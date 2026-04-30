package utils

import (
	"reflect"
	"testing"
)

func TestShellSplit(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"plain", []string{"plain"}},
		{"a b c", []string{"a", "b", "c"}},
		{`a "b c" d`, []string{"a", "b c", "d"}},
		{`a 'b c' d`, []string{"a", "b c", "d"}},
		// alias body the dogfood actually wants
		{`exec postgres -- psql -U nvoi -d nvoi -tAc "SELECT COUNT(*) FROM visits"`,
			[]string{"exec", "postgres", "--", "psql", "-U", "nvoi", "-d", "nvoi", "-tAc", "SELECT COUNT(*) FROM visits"}},
		// double-quotes inside single-quotes are literal
		{`echo 'it"s here'`, []string{"echo", `it"s here`}},
		// single-quotes inside double-quotes are literal
		{`echo "it's here"`, []string{"echo", "it's here"}},
		// adjacent quoted + unquoted concatenate into one token (POSIX behavior)
		{`a"b"c`, []string{"abc"}},
		{`'a'b'c'`, []string{"abc"}},
	}
	for _, tc := range cases {
		got, err := ShellSplit(tc.in)
		if err != nil {
			t.Errorf("ShellSplit(%q) error: %v", tc.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ShellSplit(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}

func TestShellSplit_UnbalancedQuoteErrors(t *testing.T) {
	cases := []string{
		`a "unterminated`,
		`a 'unterminated`,
		`'mismatched"`,
	}
	for _, in := range cases {
		if _, err := ShellSplit(in); err == nil {
			t.Errorf("ShellSplit(%q) expected error, got nil", in)
		}
	}
}

