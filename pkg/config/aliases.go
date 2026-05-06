package config

import (
	"fmt"
	"strings"

	"github.com/getnvoi/core/pkg/internal/utils"
)

// ExpandAlias rewrites args when args[0] is a declared alias. Behavior:
//   - empty argv, or argv[0] starts with `-`, or no matching alias name
//     → return args unchanged.
//   - alias hit: tokenize the body via utils.ShellSplit and prepend the
//     resulting tokens to args[1:]. Tokenization errors surface as an
//     error wrapping the alias name.
//
// Pure: no disk, no env. The CLI loader hands a fully-resolved *Config
// down; this function does the matching + tokenization, nothing else.
// Validate has already confirmed every body is shell-splittable, so a
// non-nil error here means the operator edited the YAML between Load
// and Expand (or the file lied about being validated).
func ExpandAlias(cfg *Config, args []string) ([]string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args, nil
	}
	body, ok := cfg.Aliases[args[0]]
	if !ok {
		return args, nil
	}
	tokens, err := utils.ShellSplit(body)
	if err != nil {
		return nil, fmt.Errorf("alias %s: %w", args[0], err)
	}
	return append(tokens, args[1:]...), nil
}
