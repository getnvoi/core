package cmd

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

const doc = `{
  "components": {"schemas": {
    "SkillsAddFiles": {"properties": {
      "document_ids": {"description": "Several library documents.", "items": {"type": "string"}, "type": "array"},
      "file": {"format": "binary", "type": "string"},
      "files": {"items": {"format": "binary", "type": "string"}, "type": "array"},
      "path": {"type": "string"}}, "required": [], "type": "object"},
    "SkillsCreate": {"properties": {
      "description": {"type": "string"},
      "name": {"type": "string"},
      "slug": {"description": "Derived from the name when absent.", "type": "string"}},
      "required": ["description", "name"], "type": "object"}}},
  "paths": {
    "/api/v1/login": {"post": {"operationId": "session_create", "tags": ["session"], "summary": "Log in",
      "requestBody": {"content": {"application/json": {"schema": {"type": "object", "properties": {"email": {"type": "string"}}}}}}}},
    "/api/v1/agents": {"get": {"operationId": "agent_list", "summary": "List agents",
      "parameters": [{"in": "query", "name": "q", "required": false, "schema": {"type": "string"}}]}},
    "/api/v1/skills": {"post": {"operationId": "skill_create", "summary": "Create a skill",
      "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/SkillsCreate"}}}}}},
    "/api/v1/skills/{id}/files": {"post": {"operationId": "skill_file_create", "summary": "Add files",
      "parameters": [{"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}],
      "requestBody": {"content": {"multipart/form-data": {"schema": {"$ref": "#/components/schemas/SkillsAddFiles"}}}}}}
  }
}`

func TestOperations(t *testing.T) {
	ops, err := Operations([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := []Operation{
		{ID: "agent_list", Group: "agent", Name: "list", Summary: "List agents", Method: "GET", Path: "/api/v1/agents",
			Query: []Flag{{Name: "q", Kind: "string"}}},
		{ID: "session_create", Group: "session", Tag: "session", Name: "create", Summary: "Log in", Method: "POST", Path: "/api/v1/login",
			Body: []Flag{{Name: "email", Kind: "string"}}},
		{ID: "skill_create", Group: "skill", Name: "create", Summary: "Create a skill", Method: "POST", Path: "/api/v1/skills",
			Body: []Flag{{Name: "description", Kind: "string", Required: true}, {Name: "name", Kind: "string", Required: true}, {Name: "slug", Kind: "string", Doc: "Derived from the name when absent."}}},
		{ID: "skill_file_create", Group: "skill-file", Name: "create", Summary: "Add files", Method: "POST", Path: "/api/v1/skills/{id}/files",
			Params: []string{"id"}, Multipart: true,
			Body: []Flag{{Name: "document_ids", Kind: "array", Doc: "Several library documents."}, {Name: "file", Kind: "file"}, {Name: "files", Kind: "files"}, {Name: "path", Kind: "string"}}},
	}
	if !reflect.DeepEqual(ops, want) {
		t.Fatalf("got %#v", ops)
	}
}

func TestAnIdWithoutAGroupIsRefused(t *testing.T) {
	if _, err := Operations([]byte(`{"paths":{"/x":{"get":{"operationId":"nogroup","summary":"x"}}}}`)); err == nil {
		t.Fatal("expected an error")
	}
}

// login, logout and whoami are written by hand; the sign-in's operations must not become a `session` group.
func TestTheSignInIsNotGenerated(t *testing.T) {
	ops, err := Operations([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range generated(ops) {
		if op.Tag == "session" {
			t.Fatalf("sign-in operation kept: %s", op.Name)
		}
	}
	if len(generated(ops)) != len(ops)-1 {
		t.Fatalf("%d of %d kept", len(generated(ops)), len(ops))
	}
}

func TestTheEmbeddedDocumentBuildsARoot(t *testing.T) {
	root, err := Root(&Resolver{})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"login", "logout", "whoami", "context"} {
		if !names[want] {
			t.Fatalf("no %s command", want)
		}
	}
	for _, c := range root.Commands() {
		if c.Name() != "session" {
			continue
		}
		for _, sub := range c.Commands() {
			if sub.Name() == "whoami" || sub.Name() == "login" {
				t.Fatalf("the sign-in became session %s", sub.Name())
			}
		}
	}
}

func TestPathParamsFollowThePath(t *testing.T) {
	ops, err := Operations([]byte(`{"paths": {"/api/v1/environments/{id}/commands/{command_id}": {"delete": {
    "operationId": "environment_command_delete", "summary": "Delete a command",
    "parameters": [{"in": "path", "name": "command_id", "required": true, "schema": {"type": "string"}},
                   {"in": "path", "name": "id", "required": true, "schema": {"type": "string"}}]}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := ops[0].Params; !reflect.DeepEqual(got, []string{"id", "command_id"}) {
		t.Fatalf("got %v", got)
	}
}

// The gateway is reached from the shell as `nvoi gateway search|describe|execute`, from the embedded document.
func TestTheGatewayGroupHasItsThreeCommands(t *testing.T) {
	root, err := Root(&Resolver{})
	if err != nil {
		t.Fatal(err)
	}
	var group *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "gateway" {
			group = c
		}
	}
	if group == nil {
		t.Fatal("no gateway group")
	}
	names := map[string]bool{}
	for _, c := range group.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"search", "describe", "execute"} {
		if !names[want] {
			t.Fatalf("no gateway %s command", want)
		}
	}
}
