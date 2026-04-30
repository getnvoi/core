package utils

import "testing"

func TestResolveVar(t *testing.T) {
	env := map[string]string{"FOO": "bar", "EMPTY": ""}
	getenv := func(k string) string { return env[k] }

	cases := []struct {
		in, want string
	}{
		{"$FOO", "bar"},
		{"literal", "literal"},
		{"", ""},
		{"$", "$"},     // bare $, no name → literal
		{"$EMPTY", ""}, // declared but empty
		{"$BAR", ""},   // not in env → empty (caller decides whether that's OK)
		{"prefix$FOO", "prefix$FOO"}, // only literal $-prefix, not interpolation
	}
	for _, tc := range cases {
		if got := ResolveVar(tc.in, getenv); got != tc.want {
			t.Errorf("ResolveVar(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
