package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getnvoi/core/pkg/config"
)

// ResolveDatabaseSecrets walks cfg.Databases and resolves every
// $VAR reference in `credentials.{user, password, database}` against
// getenv, returning the additions that need to merge into the
// existing secrets map (from ResolveSecrets).
//
// Sits at the cmd/cli boundary — the only place os.Getenv is allowed
// per the architecture rule. The deploy reconciler reads from
// runtime.SecretValues (the merged map) so it never touches the
// process env directly.
//
// Hard-errors with a sorted list of every missing var, same shape as
// ResolveSecrets, so the operator sees ALL config gaps at once
// rather than fixing them one at a time.
func ResolveDatabaseSecrets(cfg *config.Config, getenv func(string) string) (map[string]string, error) {
	if len(cfg.Databases) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	missingSet := map[string]bool{}
	for _, name := range sortedDBNames(cfg.Databases) {
		db := cfg.Databases[name]
		if db.Credentials == nil {
			continue
		}
		for _, raw := range []string{db.Credentials.User, db.Credentials.Password, db.Credentials.Database} {
			key, ok := varName(raw)
			if !ok {
				continue
			}
			if _, already := out[key]; already {
				continue
			}
			v := getenv(key)
			if v == "" {
				missingSet[key] = true
				continue
			}
			out[key] = v
		}
	}
	if len(missingSet) > 0 {
		missing := make([]string, 0, len(missingSet))
		for k := range missingSet {
			missing = append(missing, k)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("databases.*.credentials: missing or empty env vars: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// varName returns ("FOO", true) for the string "$FOO". For literals
// (anything that doesn't start with `$`) returns ("", false). Bare
// `"$"` is treated as literal — same shape as utils.ResolveVar.
func varName(s string) (string, bool) {
	if len(s) > 1 && s[0] == '$' {
		return s[1:], true
	}
	return "", false
}

func sortedDBNames(m map[string]config.DatabaseSpec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
