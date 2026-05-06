package cli

import (
	"strings"

	"github.com/getnvoi/core/pkg/config"
)

var (
	aliasCachePath string
	aliasCacheCfg  *config.Config
)

func cachedConfig(path string) (*config.Config, bool) {
	if aliasCacheCfg != nil && aliasCachePath == path {
		return aliasCacheCfg, true
	}
	return nil, false
}

func CachedConfig(path string) (*config.Config, bool) {
	return cachedConfig(path)
}

// ExpandAliasArgs is the CLI-side wrapper around config.ExpandAlias.
// Extracts the config path from argv (-c / --config), loads the YAML,
// and delegates the actual expansion to the library. Load failures
// pass through silently — args are returned unchanged so cobra can
// produce its own "config not found" error in the usual place.
func ExpandAliasArgs(args []string) ([]string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args, nil
	}
	cfg, err := loadConfigForAlias(args)
	if err != nil {
		return args, nil
	}
	return config.ExpandAlias(cfg, args)
}

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
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return nil, err
	}
	aliasCachePath, aliasCacheCfg = configPath, cfg
	return cfg, nil
}
