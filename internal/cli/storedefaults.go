package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultDatabasePath returns ~/.nvoi/db.sqlite resolved against the
// current user's home directory. Used as the implicit default for
// --database when the operator doesn't pass one and no nvoi.yaml is
// present in cwd.
func DefaultDatabasePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	return filepath.Join(home, ".nvoi", "db.sqlite"), nil
}

// ExpandHome rewrites a leading "~/" to $HOME. Operators commonly
// pass --database ~/.nvoi/db.sqlite at the shell; the shell expands
// it before exec, but in scripted contexts or via Wails bindings the
// tilde may arrive verbatim.
func ExpandHome(p string) (string, error) {
	if !strings.HasPrefix(p, "~/") && p != "~" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("user home: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}

// ApplySmartDefaults fills in ConfigPath or DatabasePath when the
// operator passed neither flag. Resolution rule:
//
//   - both flags empty AND nvoi.yaml exists in cwd      → yaml mode
//   - both flags empty AND ~/.nvoi/db.sqlite exists     → store mode
//   - both flags empty AND nothing exists               → yaml mode
//     (the standard "no nvoi.yaml" error surfaces)
//   - both flags empty AND BOTH exist                   → yaml mode
//     wins by default; bothExisted=true so PrepareRuntime can warn
//     the operator and tell them how to force store mode
//
// Returns (cfgPath, dbPath, bothExisted, err). One of cfgPath / dbPath
// is always set on success — the caller no longer needs to handle
// "both empty".
func ApplySmartDefaults(configPath, databasePath string) (string, string, bool, error) {
	if configPath != "" || databasePath != "" {
		return configPath, databasePath, false, nil
	}
	const yamlDefault = "nvoi.yaml"
	dbDefault, err := DefaultDatabasePath()
	if err != nil {
		// No HOME — pick yaml so the failure mode is familiar.
		return yamlDefault, "", false, nil
	}
	yamlExists := fileExists(yamlDefault)
	dbExists := fileExists(dbDefault)
	switch {
	case yamlExists && dbExists:
		// YAML wins. Signal ambiguity so PrepareRuntime can log a
		// warning explaining how to force store mode if intended.
		return yamlDefault, "", true, nil
	case yamlExists:
		return yamlDefault, "", false, nil
	case dbExists:
		return "", dbDefault, false, nil
	default:
		return yamlDefault, "", false, nil
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
