// nvoi is a command per operation of the API, built at start from the OpenAPI document, plus
// login, logout, whoami, context, mcp add and session follow.
package main

import (
	"fmt"
	"os"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/cmd"
)

// Set by bin/publish-cli from the source digest; "dev" in a local build.
var version = "dev"

func main() {
	root, err := cmd.Root(&cmd.Resolver{Name: cmd.ContextFlag(os.Args[1:])})
	if err != nil {
		fmt.Fprintln(os.Stderr, "openapi.json:", err)
		os.Exit(1)
	}
	root.Version = version
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
