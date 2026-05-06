package utils

import (
	"fmt"
	"strings"
	"unicode"
)

// ShellSplit tokenizes s by whitespace, treating single- and
// double-quoted regions as one token. Quote characters are stripped
// from the output. Unbalanced quotes return an error.
//
// Scope is intentionally minimal — no $VAR interpolation, no
// backslash escapes, no command substitution. Alias bodies compose
// already-resolved nvoi commands; the runtime values they reference
// (services, secrets, server keys) come from elsewhere in the YAML
// and are resolved by the verbs the alias expands to. The shell
// part is just splitting tokens.
//
// Not the inverse of ShellQuote — different domains. ShellQuote
// produces strings for remote shells (kubectl exec on a master);
// ShellSplit parses operator-authored alias bodies that have one
// well-defined shape: simple quoted strings, no escapes.
func ShellSplit(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inSingle, inDouble := false, false
	hasToken := false

	for _, r := range s {
		switch {
		case inSingle:
			if r == '\'' {
				inSingle = false
			} else {
				cur.WriteRune(r)
			}
		case inDouble:
			if r == '"' {
				inDouble = false
			} else {
				cur.WriteRune(r)
			}
		case r == '\'':
			inSingle = true
			hasToken = true
		case r == '"':
			inDouble = true
			hasToken = true
		case unicode.IsSpace(r):
			if hasToken {
				out = append(out, cur.String())
				cur.Reset()
				hasToken = false
			}
		default:
			cur.WriteRune(r)
			hasToken = true
		}
	}
	if inSingle || inDouble {
		return nil, fmt.Errorf("unbalanced quote in %q", s)
	}
	if hasToken {
		out = append(out, cur.String())
	}
	return out, nil
}
