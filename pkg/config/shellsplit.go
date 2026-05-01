package config

import (
	"fmt"
	"strings"
	"unicode"
)

func shellSplit(s string) ([]string, error) {
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
