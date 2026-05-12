package tools

import (
	"context"
	"errors"
)

// ctxKey is the unexported type used for every context key in this
// package. Two-step pattern — typed key + typed getter — keeps the
// values invisible to outside packages and prevents key collisions.
type ctxKey int

const (
	keyCwd ctxKey = iota
	keyConfigPath
	keyRootJSON
	keyNvoiBinary
	keyDatabasePath
	keyKeyring
	keyProjectName
)

// Env is the typed bundle the cmd/cli boundary stashes into ctx
// before invoking provider.Loop. Tools read individual fields via the
// helpers below; pkg/agent/tools NEVER imports cmd/cli (would cycle).
//
// All fields are operator-supplied (or boundary-resolved):
//   - Cwd        : project working directory (subprocess cwd)
//   - ConfigPath : -c flag value (yaml mode) or empty (store mode)
//   - RootJSON   : --json flag (passed through to child nvoi calls)
//   - NvoiBinary : os.Executable() resolved once at boundary
//   - DatabasePath / Keyring / ProjectName : --database / --keyring /
//     --project flag values (empty in yaml mode)
type Env struct {
	Cwd          string
	ConfigPath   string
	RootJSON     bool
	NvoiBinary   string
	DatabasePath string
	Keyring      string
	ProjectName  string
}

// WithEnv returns a derived context carrying the given Env. The
// boundary calls this once per agent turn before driving the SDK loop.
func WithEnv(ctx context.Context, e Env) context.Context {
	ctx = context.WithValue(ctx, keyCwd, e.Cwd)
	ctx = context.WithValue(ctx, keyConfigPath, e.ConfigPath)
	ctx = context.WithValue(ctx, keyRootJSON, e.RootJSON)
	ctx = context.WithValue(ctx, keyNvoiBinary, e.NvoiBinary)
	ctx = context.WithValue(ctx, keyDatabasePath, e.DatabasePath)
	ctx = context.WithValue(ctx, keyKeyring, e.Keyring)
	ctx = context.WithValue(ctx, keyProjectName, e.ProjectName)
	return ctx
}

// envFromCtx pulls every typed value out at once. Tools use the
// per-field helpers below in their hot paths.
func envFromCtx(ctx context.Context) (Env, error) {
	bin, ok := ctx.Value(keyNvoiBinary).(string)
	if !ok || bin == "" {
		return Env{}, errors.New("tools: missing NvoiBinary in context (boundary forgot tools.WithEnv?)")
	}
	cwd, _ := ctx.Value(keyCwd).(string)
	return Env{
		Cwd:          cwd,
		ConfigPath:   stringFromCtx(ctx, keyConfigPath),
		RootJSON:     boolFromCtx(ctx, keyRootJSON),
		NvoiBinary:   bin,
		DatabasePath: stringFromCtx(ctx, keyDatabasePath),
		Keyring:      stringFromCtx(ctx, keyKeyring),
		ProjectName:  stringFromCtx(ctx, keyProjectName),
	}, nil
}

func stringFromCtx(ctx context.Context, k ctxKey) string {
	v, _ := ctx.Value(k).(string)
	return v
}

func boolFromCtx(ctx context.Context, k ctxKey) bool {
	v, _ := ctx.Value(k).(bool)
	return v
}
