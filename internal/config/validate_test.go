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

		// services + registry validation
		{
			name: "service requires image",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"api": {Port: 80}}
			},
			wantErr: "services.api.image: required",
		},
		{
			name: "service requires port",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"api": {Image: "nginx:alpine"}}
			},
			wantErr: "services.api.port: required",
		},
		{
			name: "service replicas must be >= 1",
			mutate: func(c *Config) {
				zero := 0
				c.Services = map[string]ServiceSpec{"api": {Image: "nginx:alpine", Port: 80, Replicas: &zero}}
			},
			wantErr: "must be >= 1",
		},
		{
			name: "build requires fully-qualified image",
			mutate: func(c *Config) {
				c.Registry = map[string]RegistryDef{"ghcr.io": {Username: "u", Password: "p"}}
				c.Services = map[string]ServiceSpec{"api": {
					Image: "api", // bare shortname — not allowed when build is set
					Port:  80,
					Build: &BuildSpec{Context: ".", Dockerfile: "Dockerfile"},
				}}
			},
			wantErr: "fully qualified tag",
		},
		{
			name: "build requires registry entry for image's host",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"api": {
					Image: "ghcr.io/myorg/api",
					Port:  80,
					Build: &BuildSpec{Context: ".", Dockerfile: "Dockerfile"},
				}}
				// no registry: block at all
			},
			wantErr: "no registry: entry for that host",
		},
		{
			name: "build with matching registry entry passes",
			mutate: func(c *Config) {
				c.Registry = map[string]RegistryDef{"ghcr.io": {Username: "u", Password: "p"}}
				c.Services = map[string]ServiceSpec{"api": {
					Image: "ghcr.io/myorg/api",
					Port:  80,
					Build: &BuildSpec{Context: ".", Dockerfile: "Dockerfile"},
				}}
			},
		},
		{
			name: "registry entry requires username",
			mutate: func(c *Config) {
				c.Registry = map[string]RegistryDef{"ghcr.io": {Password: "p"}}
			},
			wantErr: "registry.ghcr.io.username: required",
		},
		{
			name: "registry entry requires password",
			mutate: func(c *Config) {
				c.Registry = map[string]RegistryDef{"ghcr.io": {Username: "u"}}
			},
			wantErr: "registry.ghcr.io.password: required",
		},
		{
			name: "service with pre-built public image and no build is fine",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"api": {Image: "nginx:1.27-alpine", Port: 80}}
			},
		},

		// build with single-registry inference
		{
			name: "build with single registry infers host",
			mutate: func(c *Config) {
				c.Registry = map[string]RegistryDef{"docker.io": {Username: "u", Password: "p"}}
				c.Services = map[string]ServiceSpec{"web": {
					Image: "nvoi/web", // host-less; inferred from the single registry entry
					Port:  8080,
					Build: &BuildSpec{Context: ".", Dockerfile: "Dockerfile"},
				}}
			},
		},
		{
			name: "build with multiple registries and host-less image is ambiguous",
			mutate: func(c *Config) {
				c.Registry = map[string]RegistryDef{
					"docker.io": {Username: "u", Password: "p"},
					"ghcr.io":   {Username: "u", Password: "p"},
				}
				c.Services = map[string]ServiceSpec{"web": {
					Image: "nvoi/web",
					Port:  8080,
					Build: &BuildSpec{Context: ".", Dockerfile: "Dockerfile"},
				}}
			},
			wantErr: "no host prefix but multiple registries",
		},

		// secrets — top-level
		{
			name:   "valid top-level secrets",
			mutate: func(c *Config) { c.Secrets = []string{"DATABASE_URL", "API_KEY"} },
		},
		{
			name:    "empty secret entry rejected",
			mutate:  func(c *Config) { c.Secrets = []string{"", "API_KEY"} },
			wantErr: "empty entry",
		},
		{
			name:    "invalid env var name rejected",
			mutate:  func(c *Config) { c.Secrets = []string{"foo-bar"} },
			wantErr: "not a valid env var name",
		},
		{
			name:    "duplicate secret rejected",
			mutate:  func(c *Config) { c.Secrets = []string{"X", "X"} },
			wantErr: "duplicate entry",
		},

		// secrets — service whitelist
		{
			name: "service secrets ref to declared name passes",
			mutate: func(c *Config) {
				c.Secrets = []string{"DATABASE_URL"}
				c.Services = map[string]ServiceSpec{"web": {
					Image: "nginx", Port: 80, Secrets: []string{"DATABASE_URL"},
				}}
			},
		},
		{
			name: "service secrets ref to undeclared name rejected",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"web": {
					Image: "nginx", Port: 80, Secrets: []string{"NOPE"},
				}}
			},
			wantErr: `"NOPE" is not declared in top-level secrets`,
		},

		// storage
		{
			name: "service with storage passes",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"pg": {
					Image:   "postgres:16",
					Port:    5432,
					Storage: &StorageSpec{Size: "10Gi", MountPath: "/var/lib/postgresql/data"},
				}}
			},
		},
		{
			name: "storage requires size",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"pg": {
					Image:   "postgres:16",
					Port:    5432,
					Storage: &StorageSpec{MountPath: "/data"},
				}}
			},
			wantErr: "storage.size: required",
		},
		{
			name: "storage requires mountPath",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"pg": {
					Image:   "postgres:16",
					Port:    5432,
					Storage: &StorageSpec{Size: "10Gi"},
				}}
			},
			wantErr: "storage.mountPath: required",
		},

		// servers (placement)
		{
			name: "single server pin passes",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"web": {
					Image: "nginx", Port: 80, Servers: []string{"master"},
				}}
			},
		},
		{
			name: "unknown server key rejected",
			mutate: func(c *Config) {
				c.Services = map[string]ServiceSpec{"web": {
					Image: "nginx", Port: 80, Servers: []string{"ghost"},
				}}
			},
			wantErr: `"ghost" is not a defined server`,
		},
		{
			name: "multi-server with storage rejected",
			mutate: func(c *Config) {
				c.Servers["w1"] = ServerSpec{Type: "cax11", Region: "nbg1", Role: "worker"}
				c.Services = map[string]ServiceSpec{"pg": {
					Image:   "postgres:16",
					Port:    5432,
					Storage: &StorageSpec{Size: "10Gi", MountPath: "/data"},
					Servers: []string{"master", "w1"},
				}}
			},
			wantErr: "single PV can't span nodes",
		},
		{
			name: "multi-server stateless passes",
			mutate: func(c *Config) {
				c.Servers["w1"] = ServerSpec{Type: "cax11", Region: "nbg1", Role: "worker"}
				c.Services = map[string]ServiceSpec{"web": {
					Image: "nginx", Port: 80, Servers: []string{"master", "w1"},
				}}
			},
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
