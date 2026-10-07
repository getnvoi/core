package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/getnvoi/nvoi/packages/clients/cli/internal/client"
	"github.com/getnvoi/nvoi/packages/clients/cli/internal/config"
)

// Login posts email and password, and the workspace when given, and keeps the token the API
// answers as a context, named by --context, else by the workspace the token names.
func Login(r *Resolver) *cobra.Command {
	var email, password, workspace, endpoint string
	c := &cobra.Command{
		Use:          "login",
		Short:        "Log in to a workspace and keep its token as a context",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error {
			body := map[string]string{"email": email, "password": password}
			if workspace != "" {
				body["workspace"] = workspace
			}
			where := r.Endpoint(endpoint)
			_, raw, err := client.New(where, "").JSON("POST", "/api/v1/login", nil, body)
			if err != nil {
				return err
			}
			name, slug, err := keep(r, where, raw)
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "logged in to %s as context %s\n", slug, name)
			return nil
		},
	}
	c.Flags().StringVar(&email, "email", "", "the account's email")
	c.Flags().StringVar(&password, "password", "", "the account's password")
	c.Flags().StringVar(&workspace, "workspace", "",
		"the workspace slug the token is for; the account's first when absent")
	c.Flags().StringVar(&endpoint, "endpoint", "",
		"the server to log in to, instead of the context's or "+config.DefaultEndpoint)
	for _, f := range []string{"email", "password"} {
		c.MarkFlagRequired(f)
	}
	return c
}

// keep saves the token an answer holds as a context, named by --context, else by the workspace the token names.
func keep(r *Resolver, where string, raw json.RawMessage) (string, string, error) {
	var answer struct {
		Token     string `json:"token"`
		Mode      string `json:"mode"`
		Workspace struct {
			Slug string `json:"slug"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || answer.Token == "" {
		return "", "", fmt.Errorf("the answer holds no token")
	}
	file, err := config.Load()
	if err != nil {
		return "", "", err
	}
	name := r.Name
	if name == "" {
		name = answer.Workspace.Slug
		// Named after the workspace, a login never replaces a context of another server by surprise: the
		// same slug on two servers is two contexts.
		if old, found := file.Contexts[name]; found && strings.TrimRight(old.Endpoint, "/") != where {
			return "", "", fmt.Errorf("a context named %s is already for %s: name this one with --context", name, old.Endpoint)
		}
	}
	file.Set(name, config.Context{Endpoint: where, Token: answer.Token, Workspace: answer.Workspace.Slug, Mode: answer.Mode})
	return name, answer.Workspace.Slug, file.Save()
}

// Signup makes an account with its first workspace. Every new account waits on approval: the answer holds no
// token, so nothing is kept; once nvoi approves the account, `nvoi login` makes the context.
func Signup(r *Resolver) *cobra.Command {
	var email, password, workspace, endpoint string
	c := &cobra.Command{
		Use:          "signup",
		Short:        "Sign up with an email and a password: an account and its workspace, pending approval",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error {
			where := r.Endpoint(endpoint)
			body := map[string]string{"email": email, "password": password, "workspace": workspace}
			_, raw, err := client.New(where, "").JSON("POST", "/api/v1/signup", nil, body)
			if err != nil {
				return err
			}
			var answer struct {
				Workspace struct {
					Slug string `json:"slug"`
				} `json:"workspace"`
			}
			if err := json.Unmarshal(raw, &answer); err != nil {
				return fmt.Errorf("the answer is not an account")
			}
			out := c.OutOrStdout()
			fmt.Fprintf(out, "signed up: workspace %s\n", answer.Workspace.Slug)
			fmt.Fprintln(out, "your account is pending approval: once nvoi approves it, run nvoi login")
			return nil
		},
	}
	c.Flags().StringVar(&email, "email", "", "the account's email")
	c.Flags().StringVar(&password, "password", "", "the account's password, at least 12 characters")
	c.Flags().StringVar(&workspace, "workspace", "", "the new workspace's name")
	c.Flags().StringVar(&endpoint, "endpoint", "",
		"the server to sign up on, instead of the context's or "+config.DefaultEndpoint)
	for _, f := range []string{"email", "password", "workspace"} {
		c.MarkFlagRequired(f)
	}
	return c
}

// Logout revokes the token server-side, then forgets the context. The context goes whatever the
// API answers: a token the server no longer honours is worthless.
func Logout(r *Resolver) *cobra.Command {
	return &cobra.Command{
		Use:          "logout",
		Short:        "Revoke the context's token and forget it",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error {
			file, err := config.Load()
			if err != nil {
				return err
			}
			context, name, err := file.Resolve(r.name())
			if err != nil {
				return err
			}
			if _, _, err := client.New(context.Endpoint, context.Token).
				JSON("DELETE", "/api/v1/logout", nil, nil); err != nil {
				fmt.Fprintln(c.ErrOrStderr(), err)
			}
			// A token from the environment belongs to no context: there is nothing to forget.
			if _, found := file.Contexts[name]; found {
				if err := file.Remove(name); err != nil {
					return err
				}
				if err := file.Save(); err != nil {
					return err
				}
			}
			fmt.Fprintln(c.OutOrStdout(), "logged out")
			return nil
		},
	}
}

// Whoami prints the account and workspace the token names.
func Whoami(api func() (*client.Client, error)) *cobra.Command {
	return &cobra.Command{
		Use:          "whoami",
		Short:        "The account and workspace the token names",
		SilenceUsage: true,
		RunE: func(c *cobra.Command, _ []string) error {
			cl, err := api()
			if err != nil {
				return err
			}
			_, raw, err := cl.JSON("GET", "/api/v1/whoami", nil, nil)
			if err != nil {
				return err
			}
			return printJSON(raw, c.OutOrStdout())
		},
	}
}
