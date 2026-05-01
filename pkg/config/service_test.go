package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBuildSpec_YAMLForms(t *testing.T) {
	cases := []struct {
		name        string
		yaml        string
		wantContext string
		wantDF      string
	}{
		{
			name:        "bool true → cwd context",
			yaml:        "build: true",
			wantContext: ".",
			wantDF:      "Dockerfile",
		},
		{
			name:        "bool false → no build (zero value)",
			yaml:        "build: false",
			wantContext: "",
			wantDF:      "",
		},
		{
			name:        "string → context path",
			yaml:        "build: ./services/api",
			wantContext: "./services/api",
			wantDF:      "Dockerfile",
		},
		{
			name:        "mapping with explicit dockerfile",
			yaml:        "build:\n  context: ./api\n  dockerfile: Dockerfile.prod",
			wantContext: "./api",
			wantDF:      "Dockerfile.prod",
		},
		{
			name:        "mapping with empty fields → defaults",
			yaml:        "build: {}",
			wantContext: ".",
			wantDF:      "Dockerfile",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var holder struct {
				Build *BuildSpec `yaml:"build"`
			}
			if err := yaml.Unmarshal([]byte(tc.yaml), &holder); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if tc.wantContext == "" && tc.wantDF == "" {
				// "false" form — UnmarshalYAML returns without setting
				// any fields. Build is non-nil with zero-value content,
				// which ServiceSpec.HasBuild() correctly reports as false.
				if holder.Build != nil && holder.Build.Context != "" {
					t.Errorf("expected zero-value Build, got %+v", holder.Build)
				}
				return
			}
			if holder.Build == nil {
				t.Fatal("Build is nil; expected populated")
			}
			if holder.Build.Context != tc.wantContext {
				t.Errorf("Context: got %q want %q", holder.Build.Context, tc.wantContext)
			}
			if holder.Build.Dockerfile != tc.wantDF {
				t.Errorf("Dockerfile: got %q want %q", holder.Build.Dockerfile, tc.wantDF)
			}
		})
	}
}

func TestServiceSpec_HasBuild(t *testing.T) {
	cases := []struct {
		svc  ServiceSpec
		want bool
	}{
		{ServiceSpec{}, false},
		{ServiceSpec{Build: &BuildSpec{}}, false},                 // empty context = no build
		{ServiceSpec{Build: &BuildSpec{Context: "."}}, true},
		{ServiceSpec{Build: &BuildSpec{Context: "./api"}}, true},
	}
	for _, tc := range cases {
		if got := tc.svc.HasBuild(); got != tc.want {
			t.Errorf("HasBuild(%+v) = %v, want %v", tc.svc, got, tc.want)
		}
	}
}

func TestServiceSpec_ImageHost(t *testing.T) {
	cases := []struct {
		image string
		want  string
	}{
		{"ghcr.io/myorg/api", "ghcr.io"},
		{"ghcr.io/myorg/api:v1", "ghcr.io"},
		{"registry.example.com:5000/team/svc", "registry.example.com:5000"},
		{"docker.io/library/nginx", "docker.io"},
		{"nginx", ""},                         // bare shortname → no host
		{"alpine:3.19", ""},                   // bare with tag → still no host
		{"library/nginx", ""},                 // org/name with no host
		{"", ""},
	}
	for _, tc := range cases {
		got := ServiceSpec{Image: tc.image}.ImageHost()
		if got != tc.want {
			t.Errorf("ImageHost(%q) = %q, want %q", tc.image, got, tc.want)
		}
	}
}
