package main

import (
	"context"
	"errors"
	"fmt"

	internalcli "github.com/getnvoi/core/internal/cli"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/store"
	"github.com/getnvoi/core/pkg/store/keyring"
)

// openStore is the shared boundary for every store-mgmt verb (import,
// export, rekey, projects, project, secret, secrets). It:
//
//  1. Defaults --database to ~/.nvoi/db.sqlite when unset; expands ~
//  2. Resolves the keyring backend via --keyring
//  3. Gets the master key (errors with an actionable message if absent)
//  4. Opens the store, verifies the key fingerprint
//
// Returns the open *Store + the KeyRing (useful for rekey). Caller
// closes the store. setupCmd does NOT use this — it generates the key
// and runs store.Init instead.
func openStore(ctx context.Context, flags runtime.Flags) (*store.Store, keyring.KeyRing, error) {
	path, err := resolveStorePath(flags.DatabasePath)
	if err != nil {
		return nil, nil, err
	}
	kr, err := keyring.Resolve(ctx, flags.Keyring, path)
	if err != nil {
		return nil, nil, err
	}
	key, err := kr.Get(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("keyring (%s): %w", kr.Locator(), err)
	}
	if key == nil {
		return nil, nil, fmt.Errorf(
			"keyring (%s): no master key. Run `nvoi setup` first",
			kr.Locator(),
		)
	}
	st, err := store.Open(ctx, path, key)
	if err != nil {
		if errors.Is(err, store.ErrNotInitialized) {
			return nil, nil, fmt.Errorf("database %s not initialized. Run `nvoi setup` first", path)
		}
		return nil, nil, err
	}
	return st, kr, nil
}

// resolveStorePath returns the effective --database path: explicit
// flag value if set (with ~ expanded), or the implicit default
// ~/.nvoi/db.sqlite otherwise.
func resolveStorePath(flagValue string) (string, error) {
	if flagValue == "" {
		return internalcli.DefaultDatabasePath()
	}
	return internalcli.ExpandHome(flagValue)
}

// requireProject returns an actionable error when --project is unset.
// Verbs that scope to one project (import, export, secret*, secrets,
// project show/remove) call this at the top of their RunE.
func requireProject(flags runtime.Flags) error {
	if flags.ProjectName == "" {
		return errors.New("--project <name> required (use `nvoi projects` to list)")
	}
	return nil
}

// skipRuntimeAnno is the cobra annotation key used to mark verbs that
// MUST NOT have PrepareRuntime invoked on them. setup runs before any
// store exists; store-mgmt verbs open the store directly with their
// own openStore helper instead of going through PrepareRuntime.
const skipRuntimeAnno = "nvoi/skip-runtime"

func skipRuntime() map[string]string { return map[string]string{skipRuntimeAnno: "true"} }
