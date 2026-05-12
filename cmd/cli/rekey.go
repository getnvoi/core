package main

import (
	"context"
	"crypto/rand"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/getnvoi/core/pkg/store"
)

func rekeyCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "rekey",
		Short:       "Rotate the master key for the local database",
		Annotations: skipRuntime(),
		GroupID:     groupStore,
		Long: `Generates a new 32-byte master key, re-encrypts every stored
secret under it (transactional), updates the fingerprint, and installs
the new key into the keyring backend resolved via --keyring.

The old key becomes invalid once rekey completes. If the keyring
backend is read-only (env), Set fails — use --keyring file:<path>
or --keyring os for rekey.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRekey(cmd.Context(), r)
		},
	}
}

func runRekey(ctx context.Context, r *rt) error {
	st, kr, err := openStore(ctx, r.flags)
	if err != nil {
		return err
	}
	defer st.Close()

	newKey := make([]byte, store.KeySize)
	if _, err := rand.Read(newKey); err != nil {
		return fmt.Errorf("rekey: generate key: %w", err)
	}
	if err := st.Rekey(ctx, newKey); err != nil {
		return fmt.Errorf("rekey: re-encrypt secrets: %w", err)
	}
	if err := kr.Set(ctx, newKey); err != nil {
		// At this point the DB is on the new key but the keyring still
		// has the old one. Surface this clearly — the operator needs
		// to either fix the keyring backend or restore from backup.
		return fmt.Errorf("rekey: re-encrypted secrets in DB but failed to install new key into keyring (%s): %w. "+
			"The DB is now using a key that is NOT in the keyring; restore from backup or set the keyring manually",
			kr.Locator(), err)
	}
	r.log.Info(fmt.Sprintf("rekey complete; new key in %s", kr.Locator()))
	return nil
}
