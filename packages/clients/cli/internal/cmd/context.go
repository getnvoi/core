package cmd

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/client"
	"github.com/getnvoi/nvoi/packages/clients/cli/internal/config"
)

// Resolver is which context a command runs against. `Name` is the --context flag, empty unless it
// was given; the file decides otherwise. It is read per command rather than at start, so
// `login` takes effect on the next command without a reload dance. `All` builds every command whatever the
// context's mode (the docs).
type Resolver struct {
	Name string
	All  bool
}

// Mode is the mode of the context the command line resolves, by the rules every command follows: --context, else
// the machine's only context. Empty when none resolves, for a token from NVOI_TOKEN, a login made before servers
// answered it, and the docs: then every command shows.
func (r *Resolver) Mode() string {
	if r.All {
		return ""
	}
	file, err := r.file()
	if err != nil {
		return ""
	}
	context, _, err := file.Resolve(r.Name)
	if err != nil {
		return ""
	}
	return context.Mode
}

// ContextFlag is the --context a command line names. The tree is built, and completion lists its commands, before
// cobra reads the flags, so the line is read first.
func ContextFlag(args []string) string {
	for i, arg := range args {
		if arg == "--" {
			return ""
		}
		if value, found := strings.CutPrefix(arg, "--context="); found {
			return value
		}
		if arg == "--context" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (r *Resolver) file() (*config.File, error) { return config.Load() }

// name is the context a command uses: --context, else the machine's only one.
func (r *Resolver) name() string { return r.Name }

// Repository is the nvoi remote of the working repository (`<git host>/<workspace>/<repository>.git`), empty
// outside one. The API is told it, and refuses a token of another workspace there. Tests replace it.
var Repository = func(endpoint string) string {
	out, err := exec.Command("git", "config", "--get-regexp", `^remote\..*\.url$`).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && strings.HasPrefix(fields[1], GitURL(endpoint)) {
			return fields[1]
		}
	}
	return ""
}

// Client is the api client for the resolved context, or the reason there is none.
func (r *Resolver) Client() (*client.Client, error) {
	file, err := r.file()
	if err != nil {
		return nil, err
	}
	context, _, err := file.Resolve(r.Name)
	if err != nil {
		return nil, err
	}
	api := client.New(context.Endpoint, context.Token)
	api.Repository = Repository(context.Endpoint)
	api.Workspace = context.Workspace
	return api, nil
}

// Endpoint is where a request goes before a token exists: what `login` posts to.
func (r *Resolver) Endpoint(override string) string {
	if override != "" {
		return strings.TrimRight(override, "/")
	}
	file, err := r.file()
	if err != nil {
		return config.DefaultEndpoint
	}
	return file.Endpoint(r.name())
}

// Context is the group over ~/.nvoi/config.json: what is configured and which one a command uses. A bare
// `nvoi context` lists.
func Context(r *Resolver) *cobra.Command {
	group := &cobra.Command{
		Use:          "context",
		Short:        "The servers and sign-ins this machine holds",
		SilenceUsage: true,
		RunE:         func(c *cobra.Command, _ []string) error { return list(c) },
	}
	group.AddCommand(
		&cobra.Command{
			Use:          "list",
			Short:        "Every context on this machine",
			Args:         cobra.NoArgs,
			SilenceUsage: true,
			RunE:         func(c *cobra.Command, _ []string) error { return list(c) },
		},
		&cobra.Command{
			Use:          "show",
			Short:        "The context commands run against",
			Args:         cobra.NoArgs,
			SilenceUsage: true,
			RunE: func(c *cobra.Command, _ []string) error {
				file, err := r.file()
				if err != nil {
					return err
				}
				context, name, err := file.Resolve(r.name())
				if err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "%s\t%s\t%s\n", name, context.Endpoint, context.Workspace)
				return nil
			},
		},
		&cobra.Command{
			Use:          "remove <name>",
			Short:        "Forget a context, without revoking its token",
			Args:         cobra.ExactArgs(1),
			SilenceUsage: true,
			RunE: func(c *cobra.Command, args []string) error {
				file, err := r.file()
				if err != nil {
					return err
				}
				if err := file.Remove(args[0]); err != nil {
					return err
				}
				if err := file.Save(); err != nil {
					return err
				}
				fmt.Fprintf(c.OutOrStdout(), "removed %s\n", args[0])
				return nil
			},
		},
	)
	return group
}

// The contexts as columns. The token is never printed.
func list(c *cobra.Command) error {
	file, err := config.Load()
	if err != nil {
		return err
	}
	if len(file.Contexts) == 0 {
		fmt.Fprintln(c.OutOrStdout(), "no contexts — run nvoi login")
		return nil
	}
	return columns(c.OutOrStdout(), file)
}

func columns(out io.Writer, file *config.File) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "name\tendpoint\tworkspace")
	for _, name := range file.Names() {
		context := file.Contexts[name]
		fmt.Fprintf(w, "%s\t%s\t%s\n", name, context.Endpoint, context.Workspace)
	}
	return w.Flush()
}

// announce writes, before a command runs, which context it runs against, on stderr, once the machine holds
// several: a person or an agent sees it in every output. Commands that make or list contexts say theirs themselves.
func announce(c *cobra.Command, r *Resolver) {
	for k := c; k != nil; k = k.Parent() {
		switch k.Name() {
		case "login", "signup", "logout", "context", "help", "completion":
			return
		}
	}
	file, err := r.file()
	// One context leaves nothing to mistake; the line is for machines holding several.
	if err != nil || len(file.Contexts) < 2 {
		return
	}
	context, name, err := file.Resolve(r.Name)
	if err != nil {
		return
	}
	fmt.Fprintln(c.ErrOrStderr(), Announcement(name, context))
}

// Announcement is the line that says which context a command runs against. A token from NVOI_TOKEN names no
// workspace on the machine: the server's answer does.
func Announcement(name string, context config.Context) string {
	if context.Workspace == "" {
		return fmt.Sprintf("nvoi: context %s · %s", name, context.Endpoint)
	}
	return fmt.Sprintf("nvoi: context %s · workspace %s · %s", name, context.Workspace, context.Endpoint)
}
