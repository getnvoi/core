// Package config reads the contexts: each one an API, the token that opens it and the workspace
// that token names.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrNoContext means nothing names a server to talk to.
var ErrNoContext = errors.New("not logged in — run nvoi login")

const DefaultEndpoint = "https://nvoi.to"

// Context is one server and one sign-in on it.
type Context struct {
	Endpoint  string `json:"endpoint"`
	Token     string `json:"token"`
	Workspace string `json:"workspace,omitempty"`
	// The server's mode, as its sign-in answered it: selfhosted, or cloud on nvoi.to. The commands of operations the
	// document marks for another mode are hidden. Empty for a login made before servers answered it: every command shows.
	Mode string `json:"mode,omitempty"`
}

// File is ~/.nvoi/config.json: the contexts. None is "current": a machine shared by several agents would have them
// switch it under each other, so a command names its context (--context) whenever there is more than one.
type File struct {
	Contexts map[string]Context `json:"contexts"`
}

// Load reads the file. A missing one is empty; the credentials file an older build wrote becomes a
// context named "default", so a login already made keeps working.
func Load() (*File, error) {
	file := &File{Contexts: map[string]Context{}}
	data, err := os.ReadFile(path())
	switch {
	case errors.Is(err, os.ErrNotExist):
		return file, migrate(file)
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, file); err != nil {
		return nil, fmt.Errorf("%s: %w", path(), err)
	}
	if file.Contexts == nil {
		file.Contexts = map[string]Context{}
	}
	return file, nil
}

func migrate(file *File) error {
	data, err := os.ReadFile(legacyPath())
	if err != nil {
		return nil
	}
	var legacy struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(data, &legacy) != nil || legacy.Token == "" {
		return nil
	}
	file.Set("default", Context{Endpoint: DefaultEndpoint, Token: legacy.Token})
	return file.Save()
}

// Names, sorted, for the listing and for completion.
func (f *File) Names() []string {
	names := make([]string, 0, len(f.Contexts))
	for name := range f.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Set writes a context.
func (f *File) Set(name string, context Context) {
	f.Contexts[name] = context
}

// Remove drops a context.
func (f *File) Remove(name string) error {
	if _, found := f.Contexts[name]; !found {
		return f.unknown(name)
	}
	delete(f.Contexts, name)
	return nil
}

// EnvironmentName is the name a context taken from NVOI_TOKEN is announced by: it is none of the file's.
const EnvironmentName = "NVOI_TOKEN"

// Resolve answers the context a command runs against, and its name. `name` is the --context flag; without it,
// the one context the machine holds, and none is guessed among several.
//
// NVOI_TOKEN is a context of its own, for CI and scripts: its token with NVOI_API_ENDPOINT (else the default
// server), never mixed with a context of the file, whose workspace it may not share. Naming a context as well is
// refused: one command, one identity. NVOI_API_ENDPOINT alone must be the context's own server, so a context's
// token never goes to another server.
func (f *File) Resolve(name string) (Context, string, error) {
	endpoint := strings.TrimRight(os.Getenv("NVOI_API_ENDPOINT"), "/")
	if token := os.Getenv("NVOI_TOKEN"); token != "" {
		if name != "" {
			return Context{}, name, fmt.Errorf("NVOI_TOKEN and --context %s both name a token: unset NVOI_TOKEN, or drop --context", name)
		}
		if endpoint == "" {
			endpoint = DefaultEndpoint
		}
		return Context{Endpoint: endpoint, Token: token}, EnvironmentName, nil
	}
	if name == "" && len(f.Contexts) == 1 {
		name = f.Names()[0]
	}
	if name == "" && len(f.Contexts) > 1 {
		return Context{}, "", fmt.Errorf("this machine holds several contexts (%s): name one with --context", strings.Join(f.Names(), ", "))
	}
	context, found := f.Contexts[name]
	if name != "" && !found {
		return Context{}, name, f.unknown(name)
	}
	context.Endpoint = strings.TrimRight(context.Endpoint, "/")
	if context.Endpoint == "" {
		context.Endpoint = DefaultEndpoint
	}
	if context.Token == "" {
		if endpoint != "" {
			context.Endpoint = endpoint
		}
		return context, name, ErrNoContext
	}
	if endpoint != "" && endpoint != context.Endpoint {
		return Context{}, name, fmt.Errorf("NVOI_API_ENDPOINT is %s, and the context %s is for %s: unset it, or log in to %s as another context", endpoint, name, context.Endpoint, endpoint)
	}
	return context, name, nil
}

// Endpoint is where a request goes before a token exists — what `login` posts to: NVOI_API_ENDPOINT, else the
// context's server, else the default one.
func (f *File) Endpoint(name string) string {
	if endpoint := os.Getenv("NVOI_API_ENDPOINT"); endpoint != "" {
		return strings.TrimRight(endpoint, "/")
	}
	if context, found := f.Contexts[name]; found && context.Endpoint != "" {
		return strings.TrimRight(context.Endpoint, "/")
	}
	if name == "" && len(f.Contexts) == 1 {
		return f.Endpoint(f.Names()[0])
	}
	return DefaultEndpoint
}

// Save writes the file, readable by its owner only: it holds bearer tokens.
func (f *File) Save() error {
	if err := os.MkdirAll(filepath.Dir(path()), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(), append(data, '\n'), 0o600)
}

func (f *File) unknown(name string) error {
	if len(f.Contexts) == 0 {
		return fmt.Errorf("no context named %q, and none is configured — run nvoi login", name)
	}
	return fmt.Errorf("no context named %q — have %s", name, strings.Join(f.Names(), ", "))
}

func path() string { return filepath.Join(dir(), "config.json") }

// What builds before contexts wrote: one token, no endpoint.
func legacyPath() string { return filepath.Join(dir(), "credentials.json") }

func dir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".nvoi")
}
