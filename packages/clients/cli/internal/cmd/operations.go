// Package cmd builds the command tree from the OpenAPI document and runs each
// command as one request.
package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Flag is one query parameter or body field, as a --flag.
type Flag struct {
	Name     string
	Kind     string // string, integer, number, boolean, array, object, file, files
	Required bool
	Doc      string
}

// Operation is one operation of the document, as `nvoi <group> <name>`.
type Operation struct {
	ID        string // the operation id
	Mode      string // the mode of the only installation that serves it (x-nvoi-mode); empty when every one does
	Group     string
	Name      string
	Tag       string // the model the operation works on; `session` is the sign-in
	Summary   string
	Method    string
	Path      string   // "/skills/{id}/files"
	Params    []string // positional arguments, in path order
	Query     []Flag
	Body      []Flag
	Multipart bool
	Answer    *schema // the success's body, when the document declares it: what --table takes its columns from
}

type document struct {
	Paths      map[string]map[string]operation `json:"paths"`
	Components struct {
		Schemas map[string]schema `json:"schemas"`
	} `json:"components"`
}

type operation struct {
	OperationID string      `json:"operationId"`
	Mode        string      `json:"x-nvoi-mode"`
	Tags        []string    `json:"tags"`
	Summary     string      `json:"summary"`
	Parameters  []parameter `json:"parameters"`
	RequestBody *struct {
		Content map[string]struct {
			Schema schema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema *schema `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
}

type parameter struct {
	Name     string `json:"name"`
	In       string `json:"in"`
	Required bool   `json:"required"`
	Schema   schema `json:"schema"`
}

type schema struct {
	Ref         string            `json:"$ref"`
	Type        typeName          `json:"type"`
	Format      string            `json:"format"`
	Description string            `json:"description"`
	Properties  map[string]schema `json:"properties"`
	Required    []string          `json:"required"`
	Items       *schema           `json:"items"`
}

// typeName is a schema's type: OpenAPI 3.1 gives one name or a list (["integer", "null"] for a value that
// may be absent). A list reads as its first name that is not "null".
type typeName string

func (t *typeName) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*t = typeName(one)
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	for _, name := range many {
		if name != "null" {
			*t = typeName(name)
			return nil
		}
	}
	*t = ""
	return nil
}

// Operations reads every operation out of an OpenAPI document, sorted by
// group then name. The id `a.b_c` is the command `a b-c`.
func Operations(data []byte) ([]Operation, error) {
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	var ops []Operation
	for path, methods := range doc.Paths {
		for method, op := range methods {
			built, err := build(doc, path, strings.ToUpper(method), op)
			if err != nil {
				return nil, err
			}
			ops = append(ops, built)
		}
	}
	sort.Slice(ops, func(i, j int) bool {
		return ops[i].Group+" "+ops[i].Name < ops[j].Group+" "+ops[j].Name
	})
	return ops, nil
}

// An operation id is the tool's name, <model>_<verb> or utils_<thing>_<verb>: the verb is the command and the
// words before it, without utils, the group. A session is what the app calls an agent session.
func splitID(id string) (group, name string, ok bool) {
	i := strings.LastIndex(id, "_")
	if i < 0 {
		return "", "", false
	}
	model := strings.TrimPrefix(id[:i], "utils_")
	if strings.HasPrefix(model, "agent_session") {
		model = strings.TrimPrefix(model, "agent_")
	}
	return strings.ReplaceAll(model, "_", "-"), id[i+1:], true
}

func build(doc document, path, method string, op operation) (Operation, error) {
	group, name, ok := splitID(op.OperationID)
	if !ok {
		return Operation{}, fmt.Errorf("%s %s: operationId %q is not <model>_<verb>", method, path, op.OperationID)
	}
	tag := ""
	if len(op.Tags) > 0 {
		tag = op.Tags[0]
	}
	o := Operation{
		ID:      op.OperationID,
		Mode:    op.Mode,
		Group:   group,
		Name:    name,
		Tag:     tag,
		Summary: op.Summary,
		Method:  method,
		Path:    path,
		Answer:  answer(op),
	}
	for _, p := range op.Parameters {
		switch p.In {
		case "path":
			o.Params = append(o.Params, p.Name)
		case "query":
			o.Query = append(o.Query, Flag{Name: p.Name, Kind: kind(p.Schema), Required: p.Required, Doc: p.Schema.Description})
		}
	}
	// Positionals follow the path, whatever order the recorded spec lists them in.
	sort.SliceStable(o.Params, func(i, j int) bool {
		return strings.Index(path, "{"+o.Params[i]+"}") < strings.Index(path, "{"+o.Params[j]+"}")
	})
	if op.RequestBody == nil {
		return o, nil
	}
	// An operation may take several bodies (JSON, or multipart when files come too); the command reads one:
	// multipart when offered, since it carries the same fields and the files, else JSON, else the first.
	mime := ""
	for _, m := range []string{"multipart/form-data", "application/json"} {
		if _, ok := op.RequestBody.Content[m]; ok {
			mime = m
			break
		}
	}
	if mime == "" {
		for m := range op.RequestBody.Content {
			if mime == "" || m < mime {
				mime = m
			}
		}
	}
	if content, ok := op.RequestBody.Content[mime]; ok {
		s := content.Schema
		if s.Ref != "" {
			ref, found := doc.Components.Schemas[strings.TrimPrefix(s.Ref, "#/components/schemas/")]
			if !found {
				return Operation{}, fmt.Errorf("%s: %s names no schema", op.OperationID, s.Ref)
			}
			s = ref
		}
		required := map[string]bool{}
		for _, r := range s.Required {
			required[r] = true
		}
		names := make([]string, 0, len(s.Properties))
		for n := range s.Properties {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			o.Body = append(o.Body, Flag{Name: n, Kind: kind(s.Properties[n]), Required: required[n], Doc: s.Properties[n].Description})
		}
		o.Multipart = mime == "multipart/form-data"
	}
	return o, nil
}

// The body of the operation's first success (200, 201, 202) that the document gives a schema.
func answer(op operation) *schema {
	for _, status := range []string{"200", "201", "202"} {
		if r, ok := op.Responses[status]; ok {
			if c, ok := r.Content["application/json"]; ok && c.Schema != nil {
				return c.Schema
			}
		}
	}
	return nil
}

func kind(s schema) string {
	switch {
	case s.Format == "binary":
		return "file"
	case s.Type == "array" && s.Items != nil && s.Items.Format == "binary":
		return "files"
	case s.Type == "":
		return "string"
	}
	return string(s.Type)
}
