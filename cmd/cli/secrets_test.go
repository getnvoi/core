package main

import (
	"strings"
	"testing"
)

func TestResolveSecrets_Empty(t *testing.T) {
	got, err := resolveSecrets(nil, func(string) string { return "" })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil map for empty names list, got %v", got)
	}
}

func TestResolveSecrets_ResolvesAll(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL":  "postgres://u:p@db/x",
		"POSTGRES_USER": "alice",
	}
	got, err := resolveSecrets([]string{"DATABASE_URL", "POSTGRES_USER"}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["DATABASE_URL"] != "postgres://u:p@db/x" {
		t.Errorf("DATABASE_URL: %q", got["DATABASE_URL"])
	}
	if got["POSTGRES_USER"] != "alice" {
		t.Errorf("POSTGRES_USER: %q", got["POSTGRES_USER"])
	}
}

func TestResolveSecrets_MissingValuesAreReportedTogether(t *testing.T) {
	getenv := func(string) string { return "" }

	_, err := resolveSecrets([]string{"DATABASE_URL", "POSTGRES_USER", "POSTGRES_PASSWORD"}, getenv)
	if err == nil {
		t.Fatal("expected error for missing env vars")
	}
	// Sorted, all three reported in a single error.
	for _, want := range []string{"DATABASE_URL", "POSTGRES_PASSWORD", "POSTGRES_USER"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
}

func TestResolveSecrets_EmptyValueCountsAsMissing(t *testing.T) {
	env := map[string]string{"DATABASE_URL": "", "POSTGRES_USER": "alice"}
	_, err := resolveSecrets([]string{"DATABASE_URL", "POSTGRES_USER"}, func(k string) string { return env[k] })
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("expected error mentioning DATABASE_URL, got %v", err)
	}
}
