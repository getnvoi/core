package config

import (
	"strings"
	"testing"
)

// TestParseDatabaseBinding locks the prefix-based env-binding shape
// behind services.X.databases. The parser is the only place the
// default-prefix and canonical-suffix rules live, so its tests
// effectively pin the public contract.
func TestParseDatabaseBinding(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantPrefix string
		wantDB     string
		wantErr    string // empty = expect success
	}{
		{name: "bare dbname uses default prefix", in: "app", wantPrefix: "DATABASE", wantDB: "app"},
		{name: "explicit DATABASE prefix", in: "DATABASE=app", wantPrefix: "DATABASE", wantDB: "app"},
		{name: "custom prefix", in: "REPORTS=analytics", wantPrefix: "REPORTS", wantDB: "analytics"},
		{name: "underscored prefix", in: "EDGE_CACHE=edge", wantPrefix: "EDGE_CACHE", wantDB: "edge"},
		{name: "digits allowed in prefix", in: "DB2=secondary", wantPrefix: "DB2", wantDB: "secondary"},

		{name: "empty entry", in: "", wantErr: "empty entry"},
		{name: "empty prefix", in: "=app", wantErr: "empty prefix"},
		{name: "empty dbname", in: "DATABASE=", wantErr: "empty database name"},
		{name: "lowercase prefix rejected", in: "database=app", wantErr: "UPPER_CASE_WITH_UNDERSCORES"},
		{name: "mixed-case prefix rejected", in: "MyDb=app", wantErr: "UPPER_CASE_WITH_UNDERSCORES"},
		{name: "punctuation rejected", in: "DATA-BASE=app", wantErr: "UPPER_CASE_WITH_UNDERSCORES"},

		// The old single-var alias form (`DATABASE_URL=app`) is a foot-
		// gun under the new normalization — the prefix expands to ALL
		// five canonical suffixes, so the operator must drop the suffix.
		{name: "_URL suffix collision rejected", in: "DATABASE_URL=app", wantErr: "DATABASE=app instead"},
		{name: "_HOST suffix collision rejected", in: "DATABASE_HOST=app", wantErr: "DATABASE=app instead"},
		{name: "_PORT suffix collision rejected", in: "DATABASE_PORT=app", wantErr: "DATABASE=app instead"},
		{name: "_USER suffix collision rejected", in: "DATABASE_USER=app", wantErr: "DATABASE=app instead"},
		{name: "_PASSWORD suffix collision rejected", in: "DATABASE_PASSWORD=app", wantErr: "DATABASE=app instead"},
		{name: "custom prefix with _URL suffix rejected", in: "ANALYTICS_URL=reports", wantErr: "ANALYTICS=reports instead"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix, db, err := parseDatabaseBinding(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if prefix != tc.wantPrefix {
					t.Errorf("prefix = %q, want %q", prefix, tc.wantPrefix)
				}
				if db != tc.wantDB {
					t.Errorf("dbname = %q, want %q", db, tc.wantDB)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidate_UnknownDatabaseEngine locks the "registered engines"
// gate. With no engine packages blank-imported in this same-package
// test, every engine name is unknown — exercising the registry-side
// rejection path. Tests that need a registered engine live in
// validate_database_external_test.go alongside postgres (once it
// ships).
func TestValidate_UnknownDatabaseEngine(t *testing.T) {
	c := &Config{
		App:       "hello",
		Env:       "dev",
		Providers: Providers{Infra: "hetzner"},
		SSHKey:    "/tmp/x.pub",
		Servers: map[string]ServerSpec{
			"master":  {Type: "cax11", Region: "nbg1", Role: "master"},
			"db-node": {Type: "cax21", Region: "nbg1", Role: "worker"},
		},
		Databases: map[string]DatabaseSpec{
			"app": {Engine: "magic-cloud-db"},
		},
	}
	err := c.Validate()
	if err == nil {
		t.Fatalf("expected error for unknown engine, got nil")
	}
	if !strings.Contains(err.Error(), "unknown engine") {
		t.Errorf("error = %v, want substring %q", err, "unknown engine")
	}
}

// TestValidate_EmptyDatabaseEngine locks the "engine required" gate.
// An entry with no engine field hits the same registered-engines list
// in the error message so the operator sees what to pick from.
func TestValidate_EmptyDatabaseEngine(t *testing.T) {
	c := &Config{
		App:       "hello",
		Env:       "dev",
		Providers: Providers{Infra: "hetzner"},
		SSHKey:    "/tmp/x.pub",
		Servers: map[string]ServerSpec{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		},
		Databases: map[string]DatabaseSpec{
			"app": {},
		},
	}
	err := c.Validate()
	if err == nil {
		t.Fatalf("expected error for empty engine, got nil")
	}
	if !strings.Contains(err.Error(), "engine: required") {
		t.Errorf("error = %v, want substring %q", err, "engine: required")
	}
}
