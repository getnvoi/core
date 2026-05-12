package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/store"
	"github.com/getnvoi/core/pkg/store/keyring"
)

func setupCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "setup",
		Short:       "Initialize a new local database",
		Annotations: skipRuntime(),
		GroupID:     groupStore,
		Long: `Initializes a fresh SQLite database at --database <path>.
Generates a 32-byte master key and stores it via the chosen --keyring
backend. After setup, use ` + "`nvoi import -c nvoi.yaml --project <name>`" + `
to populate the store, then run the usual deploy / plan / destroy
verbs with --database --project.

Bootstrap verb — the only command that creates state out of nothing.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetup(cmd.Context(), r)
		},
	}
}

func runSetup(ctx context.Context, r *rt) error {
	path, err := resolveStorePath(r.flags.DatabasePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("setup: mkdir %s: %w", filepath.Dir(path), err)
	}

	kr, err := keyring.Resolve(ctx, r.flags.Keyring, path)
	if err != nil {
		return fmt.Errorf("%w\nPick a keyring source explicitly: --keyring os | --keyring env | --keyring file:<path>", err)
	}

	// Use the existing key when the backend already holds one
	// (env-backed flows expect the operator to export NVOI_MASTER_KEY
	// before running setup). Generate + install only when the backend
	// has no key yet.
	key, err := kr.Get(ctx)
	if err != nil {
		return fmt.Errorf("setup: read keyring (%s): %w", kr.Locator(), err)
	}
	if key == nil {
		key = make([]byte, store.KeySize)
		if _, err := rand.Read(key); err != nil {
			return fmt.Errorf("setup: generate master key: %w", err)
		}
		if err := kr.Set(ctx, key); err != nil {
			return fmt.Errorf("setup: install master key into keyring (%s): %w\n"+
				"Tip: for --keyring env, export NVOI_MASTER_KEY before setup:\n"+
				"  export NVOI_MASTER_KEY=$(openssl rand -base64 32)\n"+
				"  nvoi setup --keyring env",
				kr.Locator(), err)
		}
	}

	st, err := store.Init(ctx, path, key)
	if err != nil {
		return fmt.Errorf("setup: initialize database: %w", err)
	}
	defer st.Close()

	r.log.Info(fmt.Sprintf("database initialized: %s", path))
	r.log.Info(fmt.Sprintf("master key stored in: %s", kr.Locator()))
	r.log.Info("next: nvoi import --project <name> -c <yaml>")
	return nil
}
