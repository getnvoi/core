package config

import (
	"strings"
	"testing"

	_ "github.com/getnvoi/core/internal/providers/cloudflare" // register the bucket provider so providers.storage: cloudflare validates
)

func TestValidate(t *testing.T) {
	base := func() *Config {
		return &Config{
			App:       "hello",
			Env:       "dev",
			Providers: Providers{Infra: "hetzner"},
			SSHKey:    "/tmp/x.pub",
			Servers: map[string]ServerSpec{
				"master": {Type: "cax11", Region: "nbg1", Role: "master"},
			},
		}
	}

	cases := []struct {
		name     string
		mutate   func(*Config)
		wantErr  string // empty = expect success
	}{
		{name: "minimal valid", mutate: func(c *Config) {}},

		{name: "missing app", mutate: func(c *Config) { c.App = "" }, wantErr: "app: required"},
		{name: "missing env", mutate: func(c *Config) { c.Env = "" }, wantErr: "env: required"},
		{name: "missing infra", mutate: func(c *Config) { c.Providers.Infra = "" }, wantErr: "providers.infra: required"},
		{name: "missing ssh_key", mutate: func(c *Config) { c.SSHKey = "" }, wantErr: "ssh_key: required"},
		{name: "no servers", mutate: func(c *Config) { c.Servers = nil }, wantErr: "at least one required"},

		{
			name: "no master",
			mutate: func(c *Config) {
				c.Servers = map[string]ServerSpec{"w": {Type: "cax11", Region: "nbg1", Role: "worker"}}
			},
			wantErr: "at least one master required",
		},
		{
			name: "two masters without primary flag",
			mutate: func(c *Config) {
				c.Servers["master-2"] = ServerSpec{Type: "cax11", Region: "nbg1", Role: "master"}
			},
			wantErr: "exactly one must have primary: true",
		},
		{
			name: "two masters with single primary",
			mutate: func(c *Config) {
				c.Servers["master"] = ServerSpec{Type: "cax11", Region: "nbg1", Role: "master", Primary: true}
				c.Servers["master-2"] = ServerSpec{Type: "cax11", Region: "nbg1", Role: "master"}
			},
		},
		{
			name: "primary on worker rejected",
			mutate: func(c *Config) {
				c.Servers["w"] = ServerSpec{Type: "cax11", Region: "nbg1", Role: "worker", Primary: true}
			},
			wantErr: "primary: true only valid on role: master",
		},
		{
			name: "unknown role",
			mutate: func(c *Config) {
				c.Servers["x"] = ServerSpec{Type: "cax11", Region: "nbg1", Role: "builder"}
			},
			wantErr: "must be master or worker",
		},
		{
			name: "reserved server name",
			mutate: func(c *Config) {
				c.Servers["default"] = ServerSpec{Type: "cax11", Region: "nbg1", Role: "worker"}
			},
			wantErr: "name reserved",
		},

		{name: "storage unset is fine", mutate: func(c *Config) { c.Providers.Storage = "" }},
		{name: "storage cloudflare ok", mutate: func(c *Config) { c.Providers.Storage = "cloudflare" }},
		{
			name:    "unknown storage provider",
			mutate:  func(c *Config) { c.Providers.Storage = "no-such-thing" },
			wantErr: `unknown provider "no-such-thing"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			err := c.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestPrimaryMaster(t *testing.T) {
	cases := []struct {
		name string
		cfg  *Config
		want string
	}{
		{
			name: "lone master implicit",
			cfg: &Config{Servers: map[string]ServerSpec{
				"master": {Role: "master"},
			}},
			want: "master",
		},
		{
			name: "explicit primary among many",
			cfg: &Config{Servers: map[string]ServerSpec{
				"a": {Role: "master"},
				"b": {Role: "master", Primary: true},
				"c": {Role: "master"},
			}},
			want: "b",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.PrimaryMaster(); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}
