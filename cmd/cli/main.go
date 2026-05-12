package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	internalcli "github.com/getnvoi/core/internal/cli"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	args, err := internalcli.ExpandAliasArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	var r rt
	root := newRoot(&r)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		if r.log != nil {
			r.log.Error(err)
		} else {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		// Honor a verb's explicit non-1 exit code when it returned an
		// *exitCodeError (currently: `nvoi env check` exits 2 on missing).
		var coded *exitCodeError
		if errors.As(err, &coded) {
			os.Exit(coded.ExitCode())
		}
		os.Exit(1)
	}
}
