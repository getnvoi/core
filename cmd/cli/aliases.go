package main

import (
	"fmt"
	"strings"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/utils"
)

// expandAliasArgs intercepts argv before cobra parses it. If the
// first positional argument matches an entry in cfg.Aliases, the
// alias body is tokenized via utils.ShellSplit and spliced in.
// Anything after the alias name is appended verbatim, so
// `nvoi visits --extra-flag` expands cleanly.
//
// Best-effort by design:
//   - empty argv → return as-is (cobra prints help)
//   - first arg is a flag → return as-is (cobra handles --help, etc.)
//   - config load fails → return as-is (cobra produces its standard
//     error path; we don't double-report)
//   - first arg isn't a known alias → return as-is
//
// Validation errors on alias bodies (unbalanced quotes, etc.) ARE
// surfaced — those would silently break dispatch otherwise.
func expandAliasArgs(args []string) ([]string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args, nil
	}

	cfg, err := loadConfigForAlias(args)
	if err != nil {
		// Config doesn't exist or doesn't validate. Hand off to cobra
		// — if argv[0] is a real verb (deploy, plan, …), cobra handles
		// it. If it's an alias of a missing config, cobra's
		// "unknown command" is the right error to show.
		return args, nil
	}

	body, ok := cfg.Aliases[args[0]]
	if !ok {
		return args, nil
	}

	tokens, err := utils.ShellSplit(body)
	if err != nil {
		// Validate caught this already, but defend: if we somehow
		// reach here with an unbalanced body, fail loud.
		return nil, fmt.Errorf("alias %s: %w", args[0], err)
	}
	return append(tokens, args[1:]...), nil
}

// aliasCache holds the cfg parsed by loadConfigForAlias, keyed by the
// resolved config path. PreRunE consults this cache before re-parsing —
// alias-time and PreRunE-time validation are bit-identical (both call
// config.Load → Validate), so a cache hit eliminates the second YAML
// parse without losing any check.
//
// Module-level scope is fine: every cobra invocation runs in a fresh
// process; no concurrent access.
var (
	aliasCachePath string
	aliasCacheCfg  *config.Config
)

// cachedConfig returns (cfg, true) when loadConfigForAlias previously
// parsed `path` successfully. Returns (nil, false) otherwise. PreRunE
// uses this to skip a duplicate config.Load.
func cachedConfig(path string) (*config.Config, bool) {
	if aliasCacheCfg != nil && aliasCachePath == path {
		return aliasCacheCfg, true
	}
	return nil, false
}

// loadConfigForAlias mirrors enough of PersistentPreRunE to look up
// aliases. Reads the -c / --config flag from args (defaults to
// nvoi.yaml — same as the cobra flag default) and runs config.Load.
// Does NOT load .env: alias names + bodies are static YAML, no env
// expansion needed at this layer.
//
// On success, caches the parsed cfg so PreRunE can reuse it without
// re-parsing. Failures are NOT cached — alias expansion swallows the
// error and falls through to PreRunE, which surfaces it cleanly.
func loadConfigForAlias(args []string) (*config.Config, error) {
	configPath := "nvoi.yaml"
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-c" || args[i] == "--config":
			if i+1 < len(args) {
				configPath = args[i+1]
			}
		case strings.HasPrefix(args[i], "--config="):
			configPath = strings.TrimPrefix(args[i], "--config=")
		case strings.HasPrefix(args[i], "-c="):
			configPath = strings.TrimPrefix(args[i], "-c=")
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	aliasCachePath, aliasCacheCfg = configPath, cfg
	return cfg, nil
}
