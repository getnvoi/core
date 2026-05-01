package workload

import "sort"

func sortedStringKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func resolveVar(s string, getenv func(string) string) string {
	if len(s) > 1 && s[0] == '$' {
		return getenv(s[1:])
	}
	return s
}
