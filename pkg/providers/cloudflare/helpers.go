package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"
)

var defaultHTTPClient = &http.Client{Timeout: 30 * time.Second}

type HTTPClient struct {
	BaseURL    string
	HTTPClient *http.Client
	SetAuth    func(*http.Request)
	Label      string
}

type Request struct {
	Method string
	Path   string
	Body   any
	Result any
}

func (c *HTTPClient) Do(ctx context.Context, req Request) error {
	var reqBody io.Reader
	if req.Body != nil {
		data, err := json.Marshal(req.Body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(data)
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, c.BaseURL+req.Path, reqBody)
	if err != nil {
		return err
	}
	if c.SetAuth != nil {
		c.SetAuth(httpReq)
	}
	if req.Body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	client := c.HTTPClient
	if client == nil {
		client = defaultHTTPClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%s: read response: %w", c.Label, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &APIError{Status: resp.StatusCode, Body: string(respBody), label: c.Label}
	}

	if req.Result != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, req.Result); err != nil {
			return fmt.Errorf("%s: decode response: %w", c.Label, err)
		}
	}
	return nil
}

type APIError struct {
	Status int
	Body   string
	label  string
}

func (e *APIError) Error() string {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
			Code    string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(e.Body), &parsed) == nil && parsed.Error.Message != "" {
		return fmt.Sprintf("%s: %s (%s)", e.label, parsed.Error.Message, parsed.Error.Code)
	}
	return fmt.Sprintf("%s: %d %s", e.label, e.Status, e.Body)
}

func (e *APIError) HTTPStatus() int { return e.Status }

var ErrNotFound = errors.New("not found")

func IsNotFound(err error) bool {
	if errors.Is(err, ErrNotFound) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == 404
	}
	return false
}

func sortedStringKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
