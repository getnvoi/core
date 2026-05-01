package config

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// ParseYAML parses and validates YAML bytes into a fresh Config. The
// caller never sees a partially-populated value on error.
func ParseYAML(raw []byte) (*Config, error) {
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// DecodeYAML reads YAML from r, then parses and validates it into a
// fresh Config.
func DecodeYAML(r io.Reader) (*Config, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read yaml: %w", err)
	}
	return ParseYAML(raw)
}

// LoadFile reads + parses + validates the YAML at path. Convenience
// adapter for file-backed callers such as the CLI.
func LoadFile(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	cfg, err := ParseYAML(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Load is the backward-compatible alias for LoadFile.
func Load(path string) (*Config, error) { return LoadFile(path) }
