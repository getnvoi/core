package cli

import (
	"strings"
	"testing"
)

func TestResolveSecrets_EmptyListReturnsNil(t *testing.T) {
	got, err := ResolveSecrets(nil, func(string) string { return "" })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for empty input, got %#v", got)
	}
}

func TestResolveSecrets_AllResolvedFromEnv(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL": "postgres://u:p@db/x",
		"API_KEY":      "k123",
	}
	got, err := ResolveSecrets([]string{"API_KEY", "DATABASE_URL"}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["DATABASE_URL"] != "postgres://u:p@db/x" || got["API_KEY"] != "k123" {
		t.Errorf("got %#v", got)
	}
}

func TestResolveSecrets_MissingErrorListsAllMissing(t *testing.T) {
	getenv := func(k string) string {
		if k == "PRESENT" {
			return "ok"
		}
		return ""
	}
	_, err := ResolveSecrets([]string{"PRESENT", "B_MISSING", "A_MISSING"}, getenv)
	if err == nil {
		t.Fatal("expected error on missing env vars")
	}
	msg := err.Error()
	// Sorted output is part of the contract — operators see the same
	// list every run regardless of iteration order.
	if !strings.Contains(msg, "A_MISSING, B_MISSING") {
		t.Errorf("missing list should be sorted; got %q", msg)
	}
	if strings.Contains(msg, "PRESENT") {
		t.Errorf("present var should not appear in missing list: %q", msg)
	}
}

func TestResolveSecrets_EmptyValueCountsAsMissing(t *testing.T) {
	_, err := ResolveSecrets([]string{"X"}, func(string) string { return "" })
	if err == nil {
		t.Fatal("empty env var should be reported as missing")
	}
}
