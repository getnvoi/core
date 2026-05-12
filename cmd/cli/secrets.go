package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func secretsCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "secrets",
		Short:       "List secret names for --project (never values)",
		Annotations: skipRuntime(),
		GroupID:     groupStore,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireProject(r.flags); err != nil {
				return err
			}
			return runSecretsList(cmd.Context(), r)
		},
	}
}

func runSecretsList(ctx context.Context, r *rt) error {
	st, _, err := openStore(ctx, r.flags)
	if err != nil {
		return err
	}
	defer st.Close()
	proj, err := st.GetProject(ctx, r.flags.ProjectName)
	if err != nil {
		return err
	}
	names, err := st.ListSecretNames(ctx, proj.ID)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		r.log.Info(fmt.Sprintf("project %q has no secrets set", proj.Name))
		return nil
	}
	for _, n := range names {
		r.log.Info(n)
	}
	return nil
}

func secretCmd(r *rt) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "secret <set|unset>",
		Short:       "Set or unset a project secret",
		Annotations: skipRuntime(),
		GroupID:     groupStore,
	}
	cmd.AddCommand(secretSetCmd(r), secretUnsetCmd(r))
	return cmd
}

func secretSetCmd(r *rt) *cobra.Command {
	var fromStdin string
	cmd := &cobra.Command{
		Use:         "set NAME=VALUE [NAME=VALUE ...]",
		Short:       "Set one or more secrets (NAME=VALUE) for --project",
		Annotations: skipRuntime(),
		Long: `Sets secrets for the project named by --project. Each argument
is NAME=VALUE. The value never appears in shell history if passed via
--from-stdin <NAME>: in that case the value is read from stdin.

Examples:

  nvoi --project demo secret set HCLOUD_TOKEN=h-xxx
  echo $TOKEN | nvoi --project demo secret set --from-stdin HCLOUD_TOKEN`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireProject(r.flags); err != nil {
				return err
			}
			return runSecretSet(cmd.Context(), r, args, fromStdin)
		},
	}
	cmd.Flags().StringVar(&fromStdin, "from-stdin", "", "read VALUE for this NAME from stdin")
	return cmd
}

func runSecretSet(ctx context.Context, r *rt, args []string, fromStdin string) error {
	if fromStdin == "" && len(args) == 0 {
		return errors.New("secret set: at least one NAME=VALUE or --from-stdin NAME required")
	}
	st, _, err := openStore(ctx, r.flags)
	if err != nil {
		return err
	}
	defer st.Close()
	proj, err := st.GetProject(ctx, r.flags.ProjectName)
	if err != nil {
		return err
	}

	// Stdin path: read until EOF, trim trailing newline, set under
	// fromStdin name. Stdin and positional args are independent —
	// operator can do both in one call.
	if fromStdin != "" {
		value, err := readAllStdin()
		if err != nil {
			return err
		}
		if err := st.SetSecret(ctx, proj.ID, fromStdin, value); err != nil {
			return err
		}
		r.log.Info(fmt.Sprintf("set %s (from stdin)", fromStdin))
	}

	// Positional NAME=VALUE pairs.
	for _, a := range args {
		name, value, ok := strings.Cut(a, "=")
		if !ok {
			return fmt.Errorf("secret set: expected NAME=VALUE, got %q", a)
		}
		if name == "" {
			return fmt.Errorf("secret set: empty NAME in %q", a)
		}
		if err := st.SetSecret(ctx, proj.ID, name, value); err != nil {
			return err
		}
		r.log.Info(fmt.Sprintf("set %s", name))
	}
	return nil
}

func secretUnsetCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "unset NAME [NAME ...]",
		Short:       "Delete one or more secrets for --project",
		Annotations: skipRuntime(),
		Args:        cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireProject(r.flags); err != nil {
				return err
			}
			return runSecretUnset(cmd.Context(), r, args)
		},
	}
}

func runSecretUnset(ctx context.Context, r *rt, names []string) error {
	st, _, err := openStore(ctx, r.flags)
	if err != nil {
		return err
	}
	defer st.Close()
	proj, err := st.GetProject(ctx, r.flags.ProjectName)
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := st.DeleteSecret(ctx, proj.ID, n); err != nil {
			r.log.Warn(fmt.Sprintf("unset %s: %v", n, err))
			continue
		}
		r.log.Info(fmt.Sprintf("unset %s", n))
	}
	return nil
}

func readAllStdin() (string, error) {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var sb strings.Builder
	first := true
	for sc.Scan() {
		if !first {
			sb.WriteByte('\n')
		}
		sb.Write(sc.Bytes())
		first = false
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return sb.String(), nil
}
