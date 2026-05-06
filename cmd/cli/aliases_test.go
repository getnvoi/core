package main

import (
	"reflect"
	"testing"

	"github.com/getnvoi/core/pkg/config"
)

// expand wraps config.ExpandAlias around an inline-built Config so each
// test reads as "given these aliases, expanding this argv produces …".
// The CLI-side wrapper (ExpandAliasArgs in internal/cli) only adds disk
// loading on top — exercised live, not here.
func expand(aliases map[string]string, args []string) ([]string, error) {
	return config.ExpandAlias(&config.Config{Aliases: aliases}, args)
}

func TestAliasExpansion_ReplacesNameWithBody(t *testing.T) {
	aliases := map[string]string{
		"visits": `exec postgres -- psql -tAc "SELECT COUNT(*) FROM visits"`,
	}
	got, err := expand(aliases, []string{"visits"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"exec", "postgres", "--", "psql", "-tAc", "SELECT COUNT(*) FROM visits"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestAliasExpansion_AppendsExtraArgs(t *testing.T) {
	aliases := map[string]string{
		"weblogs": "kubectl -- logs deploy/web",
	}
	got, err := expand(aliases, []string{"weblogs", "-f", "--tail=20"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"kubectl", "--", "logs", "deploy/web", "-f", "--tail=20"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestAliasExpansion_PassesThroughUnknownName(t *testing.T) {
	aliases := map[string]string{"visits": "exec postgres -- psql"}
	got, err := expand(aliases, []string{"deploy", "--config", "alt.yaml"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"deploy", "--config", "alt.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestAliasExpansion_EmptyArgsPassThrough(t *testing.T) {
	got, err := expand(map[string]string{"x": "exec"}, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("nil argv should pass through, got %#v", got)
	}
}
