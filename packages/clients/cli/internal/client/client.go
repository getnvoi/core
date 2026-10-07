// Package client speaks HTTP to the API with a bearer.
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Client holds the endpoint and the bearer every request carries.
type Client struct {
	endpoint string
	token    string
	http     *http.Client
	// The nvoi repository the command runs in, sent as Nvoi-Repository so the API refuses a token of another
	// workspace there.
	Repository string
	// The workspace the context says its token is for, sent as Nvoi-Workspace so the API refuses a token of
	// another workspace.
	Workspace string
}

// Failure is an answer the API refused, in its own vocabulary.
type Failure struct {
	Status  int
	Code    string
	Message string
	Fields  map[string][]string
}

func (f *Failure) Error() string {
	switch {
	case f.Status == 401 && f.Message == "":
		return "run nvoi login"
	case f.Message != "":
		return f.Message
	case len(f.Fields) > 0:
		var parts []string
		for k, v := range f.Fields {
			parts = append(parts, k+": "+strings.Join(v, ", "))
		}
		sort.Strings(parts)
		return strings.Join(parts, "; ")
	case f.Code != "":
		return f.Code
	}
	return fmt.Sprintf("HTTP %d", f.Status)
}

// New returns a client for endpoint. An empty token sends no Authorization header.
func New(endpoint, token string) *Client {
	return &Client{endpoint: endpoint, token: token, http: http.DefaultClient}
}

// Endpoint is the server the client talks to.
func (c *Client) Endpoint() string { return c.endpoint }

// JSON sends body as JSON and returns the status and the raw answer. A 4xx or
// 5xx is a *Failure.
func (c *Client) JSON(method, path string, query url.Values, body any) (int, json.RawMessage, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(data)
	}
	req, err := c.request(method, path, query, reader)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req)
}

// Multipart sends fields and files as a form. Each file is a path on disk; a
// field may carry several.
func (c *Client) Multipart(method, path string, fields url.Values, files map[string][]string) (int, json.RawMessage, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, vs := range fields {
		for _, v := range vs {
			if err := w.WriteField(k, v); err != nil {
				return 0, nil, err
			}
		}
	}
	for field, paths := range files {
		for _, file := range paths {
			if err := attach(w, field, file); err != nil {
				return 0, nil, err
			}
		}
	}
	if err := w.Close(); err != nil {
		return 0, nil, err
	}
	req, err := c.request(method, path, nil, &buf)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return c.do(req)
}

// The document's paths carry their prefix (/api/v1/...), so nothing is added.
func attach(w *multipart.Writer, field, file string) error {
	part, err := w.CreateFormFile(field, filepath.Base(file))
	if err != nil {
		return err
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(part, f)
	return err
}

func (c *Client) request(method, path string, query url.Values, body io.Reader) (*http.Request, error) {
	u := c.endpoint + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/json")
	if c.Repository != "" {
		req.Header.Set("Nvoi-Repository", c.Repository)
	}
	if c.Workspace != "" {
		req.Header.Set("Nvoi-Workspace", c.Workspace)
	}
	return req, nil
}

func (c *Client) do(req *http.Request) (int, json.RawMessage, error) {
	res, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return res.StatusCode, nil, err
	}
	if res.StatusCode >= 400 {
		return res.StatusCode, data, failure(res.StatusCode, data)
	}
	return res.StatusCode, data, nil
}

// The two shapes Api::V1::BaseController renders: {error, code} and {errors: {field: [..]}}.
func failure(status int, data []byte) error {
	f := &Failure{Status: status}
	var body struct {
		Error  string              `json:"error"`
		Code   string              `json:"code"`
		Errors map[string][]string `json:"errors"`
	}
	if err := json.Unmarshal(data, &body); err == nil {
		f.Code, f.Message, f.Fields = body.Code, body.Error, body.Errors
	}
	return f
}
