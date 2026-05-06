package utils

import (
	"cmp"
	"sort"
)

// SortedKeys returns the keys of m in ascending order. Generic over
// any ordered key type. Used wherever map iteration needs to be
// deterministic — log lines, manifest renders, test fixtures.
//
// Replaces a half-dozen hand-rolled insertion sorts that all said the
// same thing in slightly different ways. One definition, one test.
func SortedKeys[K cmp.Ordered, V any](m map[K]V) []K {
	out := make([]K, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
