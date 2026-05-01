package cli

import (
	"fmt"
	"sort"
	"strings"
)

func ResolveSecrets(names []string, getenv func(string) string) (map[string]string, error) {
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
