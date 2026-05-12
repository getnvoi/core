// Package tools is the canonical agent tool catalog. Every tool wraps
// an nvoi cmd/cli verb via subprocess exec — providers translate the
// canonical Tool schema into their SDK's tool format and route
// tool_use calls back through Tool.Execute. Zero deploy logic lives
// here; the CLI is the contract, drift-proof by construction.
//
// Tools are PURE Go: no os.Getenv, no .env. They take a Context
// (cmd/cli/agent.go's cobra context) carrying everything the executor
// needs — see context.go for the typed accessors.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Tool is one named capability the agent exposes to the model.
type Tool struct {
	// Name is the function name the SDK exposes to the model.
	// Convention: nvoi_<verb> for verb-wrappers, nvoi_<noun>_<action>
	// for noun-action operations (e.g. nvoi_config_show).
	Name string

	// Description is the model-facing tool docstring. Keep terse —
	// models don't pay attention past a few sentences. State what
	// the tool does, what its inputs mean, and any contracts the
	// model should respect (e.g. "synchronous; long-running").
	Description string

	// Schema is the input JSON Schema (object). Provider translators
	// pass this through to the SDK verbatim, so the shape must be
	// valid JSON Schema as the SDK expects. Empty `properties`
	// signals a no-arg tool.
	Schema map[string]any

	// Execute runs the tool. args is the model's tool-use input,
	// already unmarshalled to RawMessage. Returns the tool_result
	// body string (typically JSON, occasionally plain text) and an
	// error. Errors here become tool_result with is_error=true so
	// the model can react — NEVER mask transient exec failures.
	Execute func(ctx context.Context, args json.RawMessage) (string, error)
}

var (
	mu       sync.RWMutex
	registry = map[string]Tool{}
)

// Register adds a Tool to the catalog. Called from each tool's init().
// Panics on duplicate-name registration: this is a wiring bug, fail
// loud at startup.
func Register(t Tool) {
	if t.Name == "" {
		panic("tools: empty Tool.Name")
	}
	if t.Execute == nil {
		panic("tools: nil Tool.Execute for " + t.Name)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[t.Name]; ok {
		panic("tools: duplicate tool " + t.Name)
	}
	registry[t.Name] = t
}

// All returns every registered Tool sorted by Name. Providers use this
// to build their SDK-native tool list at LaunchSpec construction time.
func All() []Tool {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Tool, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ByName looks up a single tool by name. Returns ok=false when the
// model calls a tool we don't know about (provider's tool_use loop
// translates this to a tool_result with an "unknown tool" error).
func ByName(name string) (Tool, bool) {
	mu.RLock()
	defer mu.RUnlock()
	t, ok := registry[name]
	return t, ok
}

// Names returns sorted tool names — for help text, debug, tests.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// jsonObject is a shorthand for the schema's top-level shape. Tool
// declarations use it for readability.
func jsonObject(properties map[string]any, required ...string) map[string]any {
	out := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// stringProp / intProp / arrayProp build the inner property shapes.
// One-line helpers so tool schemas read top-to-bottom instead of
// nested map literals.
func stringProp(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func intProp(desc string) map[string]any    { return map[string]any{"type": "integer", "description": desc} }
func arrayProp(itemType, desc string) map[string]any {
	return map[string]any{
		"type":        "array",
		"items":       map[string]any{"type": itemType},
		"description": desc,
	}
}

// errResult builds a tool_result JSON body for the model. Used when
// Execute returns an error so the model sees a structured failure
// payload rather than a stringified error.
func errResult(format string, args ...any) string {
	b, _ := json.Marshal(map[string]any{"error": fmt.Sprintf(format, args...)})
	return string(b)
}
