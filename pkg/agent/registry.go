package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Factory builds a fresh Agent instance for a registered provider.
// Returned Agents are not reused across invocations — Resolve calls
// the factory each time so providers can hold per-turn state without
// concurrency concerns.
type Factory func() Agent

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register adds a provider factory under name. Called from each
// provider's init() — same pattern as pkg/providers/registry.go and
// pkg/store/keyring/keyring.go. Panics on duplicate registration:
// double-register is a wiring bug (two init() under the same name),
// not a runtime concern, so fail loud at startup.
func Register(name string, f Factory) {
	if name == "" {
		panic("agent: empty provider name")
	}
	if f == nil {
		panic("agent: nil factory for " + name)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[name]; ok {
		panic("agent: duplicate provider " + name)
	}
	registry[name] = f
}

// Resolve returns a fresh Agent for name. Returns an error mentioning
// the registered names when the requested provider is unknown —
// covers operator typos and missing blank-imports in cmd/cli/main.go.
func Resolve(name string) (Agent, error) {
	mu.RLock()
	f, ok := registry[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("agent: unknown provider %q (registered: %s)",
			name, strings.Join(Names(), ", "))
	}
	return f(), nil
}

// Names returns the sorted list of registered provider names.
// Useful for error messages, --help dynamic lists, and tests.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
