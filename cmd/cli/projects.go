package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func projectsCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "projects",
		Short:       "List projects in the local database",
		Annotations: skipRuntime(),
		GroupID:     groupStore,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runProjectsList(cmd.Context(), r)
		},
	}
}

func runProjectsList(ctx context.Context, r *rt) error {
	st, _, err := openStore(ctx, r.flags)
	if err != nil {
		return err
	}
	defer st.Close()

	rows, err := st.ListProjects(ctx)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		r.log.Info("no projects. Run `nvoi import -c nvoi.yaml --project <name>` to add one.")
		return nil
	}
	for _, p := range rows {
		cfg, _ := p.ParsedConfig()
		app, env := "?", "?"
		if cfg != nil {
			app, env = cfg.App, cfg.Env
		}
		r.log.Info(fmt.Sprintf("%-24s  %s/%s  (id=%s)", p.Name, app, env, p.ID))
	}
	return nil
}

func projectCmd(r *rt) *cobra.Command {
	cmd := &cobra.Command{
		Use:         "project <show|remove> [name]",
		Short:       "Manage a single project (show / remove)",
		Annotations: skipRuntime(),
		GroupID:     groupStore,
	}
	cmd.AddCommand(projectShowCmd(r), projectRemoveCmd(r))
	return cmd
}

func projectShowCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "show <name>",
		Short:       "Show a project's config + secret names",
		Annotations: skipRuntime(),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectShow(cmd.Context(), r, args[0])
		},
	}
}

func runProjectShow(ctx context.Context, r *rt, name string) error {
	st, _, err := openStore(ctx, r.flags)
	if err != nil {
		return err
	}
	defer st.Close()

	proj, err := st.GetProject(ctx, name)
	if err != nil {
		return err
	}
	cfg, err := proj.ParsedConfig()
	if err != nil {
		return err
	}
	r.log.Info(fmt.Sprintf("project: %s", proj.Name))
	r.log.Info(fmt.Sprintf("  id:      %s", proj.ID))
	r.log.Info(fmt.Sprintf("  app/env: %s/%s", cfg.App, cfg.Env))
	r.log.Info(fmt.Sprintf("  infra:   %s", cfg.Providers.Infra))
	if cfg.Providers.Storage != "" {
		r.log.Info(fmt.Sprintf("  storage: %s", cfg.Providers.Storage))
	}
	if cfg.Providers.DNS != "" {
		r.log.Info(fmt.Sprintf("  dns:     %s", cfg.Providers.DNS))
	}
	names, err := st.ListSecretNames(ctx, proj.ID)
	if err != nil {
		return err
	}
	r.log.Info(fmt.Sprintf("  secrets: %d set", len(names)))
	for _, n := range names {
		r.log.Info("    " + n)
	}
	return nil
}

func projectRemoveCmd(r *rt) *cobra.Command {
	return &cobra.Command{
		Use:         "remove <name>",
		Short:       "Delete a project (cascades to its secrets, runs, sessions)",
		Annotations: skipRuntime(),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectRemove(cmd.Context(), r, args[0])
		},
	}
}

func runProjectRemove(ctx context.Context, r *rt, name string) error {
	st, _, err := openStore(ctx, r.flags)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.DeleteProject(ctx, name); err != nil {
		return err
	}
	r.log.Info(fmt.Sprintf("project %q removed", name))
	return nil
}
