package cmd

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/config"
)

func announced(t *testing.T, r *Resolver) string {
	t.Helper()
	var out bytes.Buffer
	c := &cobra.Command{Use: "list"}
	(&cobra.Command{Use: "repo"}).AddCommand(c)
	c.SetErr(&out)
	announce(c, r)
	return out.String()
}

func TestTheContextLineShowsOnceThereAreSeveral(t *testing.T) {
	home(t)
	file := contexts(t)
	file.Set("dee", config.Context{Endpoint: "https://nvoi.to", Token: "a", Workspace: "dee"})
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}
	if got := announced(t, &Resolver{}); got != "" {
		t.Fatalf("one context: %q", got)
	}
	file.Set("etudes", config.Context{Endpoint: "https://nvoi.to", Token: "b", Workspace: "etudes"})
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}
	if got := announced(t, &Resolver{Name: "etudes"}); got != "nvoi: context etudes · workspace etudes · https://nvoi.to\n" {
		t.Fatalf("several: %q", got)
	}
}
