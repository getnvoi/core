package cli

import (
	"fmt"
	"strings"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/internal/utils"
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

func ExpandAliasArgs(args []string) ([]string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args, nil
	}
	cfg, err := loadConfigForAlias(args)
	if err != nil {
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

