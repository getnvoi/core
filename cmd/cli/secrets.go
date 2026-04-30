package main

import (
	"fmt"
	"sort"
	"strings"
)

// resolveSecrets resolves every name in `names` against the supplied
// getenv function (os.Getenv at the cmd/ boundary; substituted in
// tests). Missing or empty values are collected and reported in one
// error so the operator sees the full list to populate, not one at a
// time.
//
// Lives at the cmd/ boundary because it reads the process environment
// — internal packages stay env-free. The resolved map flows
// downstream via runtime.Inputs.SecretValues.
func resolveSecrets(names []string, getenv func(string) string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(names))
	var missing []string
	for _, name := range names {
		v := getenv(name)
		if v == "" {
			missing = append(missing, name)
			continue
		}
		out[name] = v
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("secrets: missing or empty env vars: %s", strings.Join(missing, ", "))
	}
	return out, nil
}
