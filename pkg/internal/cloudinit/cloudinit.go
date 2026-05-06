// Package cloudinit renders cloud-init user-data shared across every
// IaaS provider (hetzner / aws / scaleway). Mirrors the shape of
// `../nvoi/pkg/infra/cloudinit.go::RenderCloudInit` so porting the rest
// of nvoi's bootstrap surface (k3s install, swap, packages) is mechanical.
//
// The provider's own emitter is responsible for any per-provider envelope
// (Hetzner takes raw user_data; AWS base64-encodes; Scaleway uses
// `cloud_init`). The cloud-init payload itself is provider-agnostic.
package cloudinit

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/getnvoi/core/pkg/naming"
)

type config struct {
	Hostname    string `yaml:"hostname,omitempty"`
	Users       []user `yaml:"users"`
	DisableRoot bool   `yaml:"disable_root"`
	SSHPwAuth   bool   `yaml:"ssh_pwauth"`
}

type user struct {
	Name              string   `yaml:"name"`
	Sudo              string   `yaml:"sudo"`
	Shell             string   `yaml:"shell"`
	LockPasswd        bool     `yaml:"lock_passwd"`
	SSHAuthorizedKeys []string `yaml:"ssh_authorized_keys"`
}

// Render produces the `#cloud-config` user-data string. hostname sets
// the machine hostname (drives the k3s node name when k3s lands).
func Render(sshPublicKey, hostname string) (string, error) {
	cfg := config{
		Hostname: hostname,
		Users: []user{{
			Name:              naming.DefaultUser,
			Sudo:              "ALL=(ALL) NOPASSWD:ALL",
			Shell:             "/bin/bash",
			LockPasswd:        true,
			SSHAuthorizedKeys: []string{sshPublicKey},
		}},
		DisableRoot: true,
		SSHPwAuth:   false,
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("cloud-init: %w", err)
	}
	return "#cloud-config\n" + string(data), nil
}
