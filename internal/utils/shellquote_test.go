package utils

import "testing"

func TestShellQuote(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", "''"},
		{"plain", "'plain'"},
		{"with space", "'with space'"},
		{"with 'inner' quote", `'with '\''inner'\'' quote'`},
		{`SELECT COUNT(*) FROM visits`, `'SELECT COUNT(*) FROM visits'`},
		{`it's a "thing"`, `'it'\''s a "thing"'`},
		// Wildcards stay literal — the whole point.
		{`*.go`, `'*.go'`},
		{`$HOME`, `'$HOME'`},
		{`a;b|c&d`, `'a;b|c&d'`},
	}
	for _, tc := range cases {
		got := ShellQuote(tc.in)
		if got != tc.want {
			t.Errorf("ShellQuote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
