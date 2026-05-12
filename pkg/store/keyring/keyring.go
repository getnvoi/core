// Package keyring is the provider abstraction for the master key
// behind pkg/store's secret encryption. Backends:
//
//   - os    — github.com/zalando/go-keyring (macOS Keychain, Windows
//             Credential Manager, Linux Secret Service / libsecret)
//   - env   — process env var, default NVOI_MASTER_KEY
//   - file  — plaintext (chmod 0600) or age-encrypted file (.age)
//
// Activation: --keyring <spec> at the cmd/cli boundary. Resolved once
// at startup; the returned KeyRing is handed to store.Open / store.Init.
//
// Same registry pattern as pkg/providers/registry.go: backends self-
// register from init(), Resolve looks up by name. Adding a future
// backend (kms, vault, hsm) is one Register call.
package keyring

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// KeyRing is the contract every backend implements. Goroutine-safe is
// not required — Resolve returns a fresh KeyRing per call and the
// boundary uses it serially.
type KeyRing interface {
	// Get returns the 32-byte master key. Implementations decode their
	// storage layer (base64 for text-based backends; age for .age
	// files; raw from go-keyring). A return of (nil, nil) — distinct
	// from an error — means "no key found in this backend"; the
	// `auto` resolver uses this to fall back to env.
	Get(ctx context.Context) ([]byte, error)

	// Set installs a new master key. Backends that are read-only
	// (env, today) return an explicit error directing the operator to
	// set the value via their layer instead.
	Set(ctx context.Context, key []byte) error

	// Locator is a non-secret descriptor used in error messages and
	// `nvoi db init` output. Examples:
	//   "os:keychain:master:5b8a..."
	//   "env:NVOI_MASTER_KEY"
	//   "file:~/.config/nvoi/master.key"
	//   "file:~/.config/nvoi/master.key.age"
	Locator() string
}

// Spec is the parsed --keyring flag handed to backend factories. The
// dbPath is supplied so backends that derive an account name from the
// database (os) get a stable, unique identifier per database.
type Spec struct {
	Backend string // "auto" | "os" | "env" | "file"
	Arg     string // backend-specific: env var name for env; file path for file; ignored for os/auto
	DBPath  string // resolved --database path
}

// Factory builds a KeyRing from a Spec.
type Factory func(spec Spec) (KeyRing, error)

var (
	mu       sync.RWMutex
	registry = map[string]Factory{}
)

// Register adds a backend factory under name. Called from each
// backend's init(). Panics on duplicate registration — wiring bug.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[name]; ok {
		panic("keyring: duplicate backend " + name)
	}
	registry[name] = f
}

// Names returns sorted backend names — for error messages and the
// settings UI.
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

// Resolve parses raw into a Spec, looks up the registered factory,
// invokes it. The "auto" alias tries os first, falls back to env.
//
// Forms accepted:
//
//	""            same as "auto"
//	"auto"        try os; on (nil, nil), fall back to env
//	"os"          force os backend
//	"env"         force env backend, var = NVOI_MASTER_KEY
//	"env:NAME"    force env backend, var = NAME
//	"file:<path>" file backend; .age suffix triggers age decryption
func Resolve(ctx context.Context, raw, dbPath string) (KeyRing, error) {
	if raw == "" || raw == "auto" {
		return resolveAuto(ctx, dbPath)
	}
	spec, err := parseSpec(raw, dbPath)
	if err != nil {
		return nil, err
	}
	mu.RLock()
	f, ok := registry[spec.Backend]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("keyring: unknown backend %q (registered: %s)",
			spec.Backend, strings.Join(Names(), ", "))
	}
	return f(spec)
}

// parseSpec splits the raw flag into a Spec. Backend names are matched
// before any `:` separator.
func parseSpec(raw, dbPath string) (Spec, error) {
	backend, arg, _ := strings.Cut(raw, ":")
	switch backend {
	case "os":
		return Spec{Backend: "os", DBPath: dbPath}, nil
	case "env":
		return Spec{Backend: "env", Arg: arg, DBPath: dbPath}, nil
	case "file":
		if arg == "" {
			return Spec{}, fmt.Errorf("keyring: file backend requires a path (e.g. --keyring file:/path/to/master.key)")
		}
		return Spec{Backend: "file", Arg: arg, DBPath: dbPath}, nil
	default:
		return Spec{}, fmt.Errorf("keyring: unknown backend %q (use one of: %s)",
			backend, strings.Join(Names(), ", "))
	}
}

// resolveAuto tries os; on "no key found" (Get returns nil, nil), tries
// env. Returns the first backend that yields a key. Both empty: error
// with explicit next-step instructions.
func resolveAuto(ctx context.Context, dbPath string) (KeyRing, error) {
	mu.RLock()
	osF, hasOS := registry["os"]
	envF, hasEnv := registry["env"]
	mu.RUnlock()

	if hasOS {
		kr, err := osF(Spec{Backend: "os", DBPath: dbPath})
		if err == nil {
			key, err := kr.Get(ctx)
			if err != nil {
				return nil, fmt.Errorf("keyring: auto: %w", err)
			}
			if key != nil {
				return kr, nil
			}
		}
	}
	if hasEnv {
		kr, err := envF(Spec{Backend: "env", DBPath: dbPath})
		if err == nil {
			key, err := kr.Get(ctx)
			if err == nil && key != nil {
				return kr, nil
			}
		}
	}
	return nil, fmt.Errorf("keyring: no master key found via OS keychain or NVOI_MASTER_KEY env. " +
		"Run `nvoi db init` to generate one, or set --keyring explicitly " +
		"(os | env[:VAR] | file:<path>).")
}
