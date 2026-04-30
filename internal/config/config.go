// Package config owns the parsed YAML. Pure data — no flags, no
// runtime, no env reads, no disk I/O beyond Load. Read-only after Load
// returns; every internal package downstream trusts a *Config that came
// out of Load.
//
// Path resolution (tilde expansion, file existence, key reads) lives
// at the cmd/ boundary. Validate (in validate.go) checks YAML shape
// + provider registration only.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config is the parsed YAML. One struct, one shape, mirrors the
// upstream nvoi.yaml surface so porting is mechanical.
type Config struct {
	App       string                `yaml:"app"`
	Env       string                `yaml:"env"`
	Providers Providers             `yaml:"providers"`
	SSHKey    string                `yaml:"ssh_key"` // path to public key — resolved + read at the cmd/ boundary
	Servers   map[string]ServerSpec `yaml:"servers"`
}

type Providers struct {
	Infra string `yaml:"infra"`

	// Storage is OPTIONAL. When unset, terraform state lives locally
	// in `.tf/<app>-<env>/terraform.tfstate`. When set, the bucket
	// `nvoi-{app}-{env}-tfstate` is auto-provisioned at deploy time
	// and terraform's `s3` backend points at it. Transitions both
	// directions auto-migrate state.
	Storage string `yaml:"storage,omitempty"`
}

// ServerSpec describes one server. Role is `master` or `worker`.
// Primary marks the master that runs `--cluster-init` on cold-start
// bootstrap (writes the etcd cluster). Required only when ≥2 masters;
// implicit on a single master.
type ServerSpec struct {
	Type    string `yaml:"type"`
	Region  string `yaml:"region"`
	Role    string `yaml:"role"`
	Primary bool   `yaml:"primary,omitempty"`
}

// Load reads + parses + validates the YAML at path. Returns a fresh
// *Config; the caller never sees a partially-populated value on error.
// The single os.ReadFile call here is the one legitimate I/O in this
// package.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// PrimaryMaster returns the YAML key of the master that runs --cluster-init
// on cold-start bootstrap. For multi-master clusters that's the one with
// `primary: true`. For single-master clusters it's the lone master.
// Caller may assume Validate has already succeeded.
func (c *Config) PrimaryMaster() string {
	var lone string
	for name, srv := range c.Servers {
		if srv.Role != "master" {
			continue
		}
		if srv.Primary {
			return name
		}
		lone = name
	}
	return lone // single-master implicit
}
