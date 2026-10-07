package cmd

import (
	_ "embed"

	"github.com/spf13/cobra"
)

// The OpenAPI document, nvoi.to's priv/openapi.yaml as JSON, written here by packages/saas/bin/generate.
// go:embed cannot read outside the module.
//
//go:embed openapi.json
var openapi []byte

// Root is the whole tree: login, logout, whoami, context, mcp, signup, one group per tag of the document,
// and follow under session, deployment and image. Every command takes --context, which names the server and token for that
// one call, and is required once the machine holds more than one. On a context whose server is of another mode, the
// operations the document marks for one mode are hidden (Tree): r.Name holds the command line's --context first
// (ContextFlag).
func Root(r *Resolver) (*cobra.Command, error) {
	ops, err := Operations(openapi)
	if err != nil {
		return nil, err
	}
	// Read before the flag is declared, which sets r.Name to its default until cobra parses the line.
	mode := r.Mode()
	root := &cobra.Command{Use: "nvoi", Short: "The nvoi API from a shell", SilenceErrors: true,
		PersistentPreRun: func(c *cobra.Command, _ []string) { announce(c, r) }}
	root.PersistentFlags().StringVar(&r.Name, "context", "",
		"the context this command runs against; required when the machine holds several")
	root.AddCommand(Login(r), Logout(r), Whoami(r.Client), Context(r), MCP(r, LocalMachine()), Signup(r))
	Tree(root, generated(ops), r.Client, mode)
	for _, group := range root.Commands() {
		if group.Use == "session" {
			group.AddCommand(Follow(r.Client))
		}
		if group.Use == "deployment" {
			group.AddCommand(FollowLines(r.Client, deploymentLog))
		}
		if group.Use == "image" {
			group.AddCommand(FollowLines(r.Client, imageLog))
		}
		if group.Use == "database" {
			group.AddCommand(FollowLines(r.Client, databaseLog))
		}
	}
	return root, nil
}

// The operations the tree is built from: every one but the sign-in's, whose three commands are written by
// hand above.
func generated(ops []Operation) []Operation {
	var kept []Operation
	for _, op := range ops {
		if op.Tag == "session" {
			continue
		}
		kept = append(kept, op)
	}
	return kept
}
