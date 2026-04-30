package main

import (
	"reflect"
	"testing"

	"github.com/getnvoi/core/internal/utils"
)

// expandFromAliases is the testable core of expandAliasArgs minus
// the disk read. Same logic, takes a pre-parsed aliases map.
func expandFromAliases(aliases map[string]string, args []string) ([]string, error) {
	if len(args) == 0 {
		return args, nil
	}
	body, ok := aliases[args[0]]
	if !ok {
		return args, nil
	}
	tokens, err := utils.ShellSplit(body)
	if err != nil {
		return nil, err
	}
	return append(tokens, args[1:]...), nil
}

func TestAliasExpansion_ReplacesNameWithBody(t *testing.T) {
	aliases := map[string]string{
		"visits": `exec postgres -- psql -tAc "SELECT COUNT(*) FROM visits"`,
	}
	got, err := expandFromAliases(aliases, []string{"visits"})
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
	got, err := expandFromAliases(aliases, []string{"weblogs", "-f", "--tail=20"})
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
	got, err := expandFromAliases(aliases, []string{"deploy", "--config", "alt.yaml"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := []string{"deploy", "--config", "alt.yaml"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
}

func TestAliasExpansion_EmptyArgsPassThrough(t *testing.T) {
	got, err := expandFromAliases(map[string]string{"x": "exec"}, nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Errorf("nil argv should pass through, got %#v", got)
	}
}
