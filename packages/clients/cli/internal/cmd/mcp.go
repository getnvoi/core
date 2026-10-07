package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/client"
)

// The name of the server entry written into a client's configuration.
const serverName = "nvoi"

// A personal token's life in days when --expires-in-days is not given; the server's own default.
const defaultTokenDays = 90

// Clients is what `mcp add` writes to, by the argument that names it.
var clients = map[string]string{"claude": "Claude Code", "cursor": "Cursor"}

// Machine is what `mcp add` reaches outside the API: programs, the home directory and the host name.
// Tests replace it.
type Machine struct {
	Run      func(name string, args ...string) ([]byte, error)
	LookPath func(name string) (string, error)
	Home     func() (string, error)
	Hostname func() (string, error)
}

// LocalMachine is the machine the CLI runs on.
func LocalMachine() *Machine {
	return &Machine{
		Run: func(name string, args ...string) ([]byte, error) {
			return exec.Command(name, args...).CombinedOutput()
		},
		LookPath: exec.LookPath,
		Home:     os.UserHomeDir,
		Hostname: os.Hostname,
	}
}

// MCPURL is the MCP server for an API endpoint: the MCP host beside it.
func MCPURL(endpoint string) string { return Beside(endpoint, "mcp") }

// Token is the record POST /api/v1/tokens answers, with its secret.
type Token struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ExpiresAt string `json:"expires_at"`
	Secret    string `json:"secret"`
}

// TokenRequest is the body of POST /api/v1/tokens. An empty workspace is left out: the server takes the
// token's own.
func TokenRequest(name, workspace string, days int) map[string]any {
	body := map[string]any{"name": name, "expires_in_days": days}
	if workspace != "" {
		body["workspace"] = workspace
	}
	return body
}

// TokenName is the name the person sees in their tokens: the client and this machine's short host name.
func TokenName(client, hostname string) string {
	hostname, _, _ = strings.Cut(hostname, ".")
	if hostname == "" {
		return client
	}
	return fmt.Sprintf("%s (%s)", client, hostname)
}

func createToken(api *client.Client, body map[string]any) (Token, error) {
	_, raw, err := api.JSON("POST", "/api/v1/tokens", nil, body)
	if err != nil {
		return Token{}, err
	}
	var answer struct {
		Token Token `json:"token"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Token.Secret == "" {
		return Token{}, fmt.Errorf("the answer holds no token secret")
	}
	return answer.Token, nil
}

// ClaudeArgs is the `claude` command line that adds the server with the token as its header, and the workspace
// the token is for (Nvoi-Workspace), which nvoi checks: a token of another workspace put there later is refused.
func ClaudeArgs(url, secret, scope, workspace string) []string {
	args := []string{"mcp", "add", "--transport", "http", "--scope", scope, serverName, url,
		"--header", "Authorization: Bearer " + secret}
	if workspace != "" {
		args = append(args, "--header", "Nvoi-Workspace: "+workspace)
	}
	return args
}

// The command line as a person types it, for printing.
func shellLine(name string, args []string) string {
	parts := []string{name}
	for _, a := range args {
		if strings.ContainsAny(a, " \"'$") {
			a = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

// CursorPath is Cursor's global configuration under home.
func CursorPath(home string) string { return filepath.Join(home, ".cursor", "mcp.json") }

// CursorEntry is the server entry Cursor reads, with the workspace the token is for, as for Claude Code.
func CursorEntry(url, secret, workspace string) map[string]any {
	headers := map[string]any{"Authorization": "Bearer " + secret}
	if workspace != "" {
		headers["Nvoi-Workspace"] = workspace
	}
	return map[string]any{"url": url, "headers": headers}
}

// ErrSharedScope refuses Claude Code's project scope: it writes the server into the repository's .mcp.json, which
// is committed, so the secret would leave the machine and everyone who clones would act as the person who added it.
var ErrSharedScope = errors.New("--scope project writes the token into the repository's .mcp.json, shared with " +
	"everyone who clones it: use local (this folder, the default) or user (every folder on this machine)")

// ErrServerExists refuses to replace an nvoi server already configured.
var ErrServerExists = errors.New("an nvoi server is already configured; --force replaces it")

// ReadCursor reads Cursor's configuration, an empty one when the file is missing. With force false, an
// nvoi server already in it is refused.
func ReadCursor(path string, force bool) (map[string]any, error) {
	file := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return file, nil
	case err != nil:
		return nil, err
	}
	if strings.TrimSpace(string(data)) != "" {
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	servers, _ := file["mcpServers"].(map[string]any)
	if _, found := servers[serverName]; found && !force {
		return nil, fmt.Errorf("%s: %w", path, ErrServerExists)
	}
	return file, nil
}

// WriteCursor sets the nvoi server in file, keeping every other entry, and writes it readable by its owner
// only: it holds a bearer token.
func WriteCursor(path string, file map[string]any, entry map[string]any) error {
	servers, _ := file["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[serverName] = entry
	file["mcpServers"] = servers
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	next, err := os.CreateTemp(filepath.Dir(path), ".mcp.json.")
	if err != nil {
		return err
	}
	defer os.Remove(next.Name())
	if _, err := next.Write(append(data, '\n')); err != nil {
		next.Close()
		return err
	}
	if err := next.Chmod(0o600); err != nil {
		next.Close()
		return err
	}
	if err := next.Close(); err != nil {
		return err
	}
	return os.Rename(next.Name(), path)
}

// MCP is the group that connects MCP clients to nvoi.
func MCP(r *Resolver, m *Machine) *cobra.Command {
	group := &cobra.Command{Use: "mcp", Short: "Connect MCP clients to nvoi"}
	group.AddCommand(mcpAdd(r, m))
	return group
}

type addOptions struct {
	print, force bool
	scope, url   string
	days         int
}

func mcpAdd(r *Resolver, m *Machine) *cobra.Command {
	var o addOptions
	c := &cobra.Command{
		Use:   "add [claude|cursor]",
		Short: "Make a personal token and write the nvoi MCP server into Claude Code or Cursor",
		Long: "Makes a personal token for the context's workspace and writes the nvoi MCP server, with that " +
			"token, into the client's configuration. --context names the context, and is required when the " +
			"machine holds several. Inside a repository nvoi hosts, a context of another workspace is refused. " +
			"--print writes nothing and prints the token and the configuration instead.",
		Args: func(c *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("one client: claude or cursor")
			}
			if len(args) == 0 {
				if o.print {
					return nil
				}
				return fmt.Errorf("name a client: claude or cursor (or --print)")
			}
			if _, found := clients[args[0]]; !found {
				return fmt.Errorf("unknown client %q: claude or cursor", args[0])
			}
			return nil
		},
		ValidArgs:    []string{"claude", "cursor"},
		SilenceUsage: true,
		RunE: func(c *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			return add(c, r, m, name, o)
		},
	}
	c.Flags().BoolVar(&o.print, "print", false, "write nothing; print the token and the configuration")
	c.Flags().BoolVar(&o.force, "force", false, "replace an nvoi server already configured")
	// local: Claude keeps the server per project folder, so each repository and worktree holds its own
	// workspace's token; user would give every project on the machine one workspace.
	c.Flags().StringVar(&o.scope, "scope", "local", "claude only: local (this folder) or user (every folder); project is refused, it would commit the token")
	c.Flags().StringVar(&o.url, "url", "", "the MCP server, instead of the one of the context's endpoint")
	c.Flags().IntVar(&o.days, "expires-in-days", defaultTokenDays, "the token's life in days, 1 to 365")
	return c
}

func add(c *cobra.Command, r *Resolver, m *Machine, name string, o addOptions) error {
	out := c.OutOrStdout()
	if name != "cursor" && o.scope == "project" {
		return ErrSharedScope
	}
	file, err := r.file()
	if err != nil {
		return err
	}
	context, _, err := file.Resolve(r.name())
	if err != nil {
		return err
	}
	url := o.url
	if url == "" {
		url = MCPURL(context.Endpoint)
	}

	// Checked before the token is made, so a refusal leaves no token behind.
	var cursorPath string
	var cursorFile map[string]any
	claudeFound := false
	if !o.print {
		switch name {
		case "cursor":
			home, err := m.Home()
			if err != nil {
				return err
			}
			cursorPath = CursorPath(home)
			if cursorFile, err = ReadCursor(cursorPath, o.force); err != nil {
				return err
			}
		case "claude":
			if _, err := m.LookPath("claude"); err == nil {
				claudeFound = true
				if _, err := m.Run("claude", "mcp", "get", serverName); err == nil && !o.force {
					return fmt.Errorf("claude: %w", ErrServerExists)
				}
			}
		}
	}

	hostname, _ := m.Hostname()
	label := "MCP"
	if name != "" {
		label = clients[name]
	}
	// The resolver's client says the repository this runs in, and the context's workspace: nvoi refuses to make
	// the token when either is another workspace's, before anything is written.
	api, err := r.Client()
	if err != nil {
		return err
	}
	token, err := createToken(api, TokenRequest(TokenName(label, hostname), context.Workspace, o.days))
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "made the token %q (%s), expires %s\n", token.Name, token.ID, token.ExpiresAt)

	switch {
	case o.print:
		printConfig(out, name, url, token.Secret, o.scope, context.Workspace)
	case name == "cursor":
		if err := WriteCursor(cursorPath, cursorFile, CursorEntry(url, token.Secret, context.Workspace)); err != nil {
			return revoke(api, token, err)
		}
		fmt.Fprintf(out, "wrote the nvoi server (%s) to %s\n", url, cursorPath)
		fmt.Fprintln(out, everywhere(context.Workspace, "every Cursor project on this machine"))
	case name == "claude" && !claudeFound:
		fmt.Fprintln(out, "claude is not on PATH; run this command to add the server:")
		fmt.Fprintln(out, shellLine("claude", ClaudeArgs(url, token.Secret, o.scope, context.Workspace)))
	case name == "claude":
		if o.force {
			// Absent in this scope is not a failure: the add below is what counts.
			m.Run("claude", "mcp", "remove", "--scope", o.scope, serverName)
		}
		if said, err := m.Run("claude", ClaudeArgs(url, token.Secret, o.scope, context.Workspace)...); err != nil {
			return revoke(api, token, fmt.Errorf("claude mcp add: %v: %s", err, strings.TrimSpace(string(said))))
		}
		fmt.Fprintf(out, "added the nvoi server (%s) to Claude Code, scope %s\n", url, o.scope)
		if o.scope == "user" {
			fmt.Fprintln(out, everywhere(context.Workspace, "every Claude Code project on this machine without a local nvoi server"))
		}
	}
	fmt.Fprintf(out, "revoke it with: nvoi token delete %s\n", token.ID)
	return nil
}

// everywhere says that a configuration shared by every folder acts in one workspace, and how to scope it.
func everywhere(workspace, where string) string {
	if workspace == "" {
		workspace = "the token's workspace"
	}
	return fmt.Sprintf("%s now acts in %s; a repository of another workspace needs its own server "+
		"(nvoi mcp add claude --context <name>, scope local, in that folder)", where, workspace)
}

// revoke deletes a token the client could not be given, and answers the reason it could not.
func revoke(api *client.Client, token Token, reason error) error {
	if _, _, err := api.JSON("DELETE", "/api/v1/tokens/"+token.ID, nil, nil); err != nil {
		return fmt.Errorf("%w; the token %s stays: nvoi token delete %s", reason, token.ID, token.ID)
	}
	return fmt.Errorf("%w; the token was revoked", reason)
}

// printConfig prints the secret and the configuration of one client, or of both when none is named.
func printConfig(out io.Writer, name, url, secret, scope, workspace string) {
	fmt.Fprintf(out, "secret: %s\nurl: %s\n", secret, url)
	if name == "" || name == "claude" {
		fmt.Fprintf(out, "\nClaude Code:\n%s\n", shellLine("claude", ClaudeArgs(url, secret, scope, workspace)))
	}
	if name == "" || name == "cursor" {
		data, _ := json.MarshalIndent(map[string]any{
			"mcpServers": map[string]any{serverName: CursorEntry(url, secret, workspace)},
		}, "", "  ")
		fmt.Fprintf(out, "\nCursor (~/.cursor/mcp.json):\n%s\n", data)
	}
}
