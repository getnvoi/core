package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func exportCmd(r *rt) *cobra.Command {
	var outDir string
	var includeRedacted bool
	cmd := &cobra.Command{
		Use:         "export",
		Short:       "Export a project as yaml + redacted .env",
		Annotations: skipRuntime(),
		GroupID:     groupStore,
		Long: `Writes <out>/nvoi.yaml from the stored config and (when
--with-secrets) <out>/.env.redacted with one KEY= line per stored
secret name (no values, never). The yaml is the same shape as the
original nvoi.yaml — round-trippable with ` + "`nvoi import`" + `.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireProject(r.flags); err != nil {
				return err
			}
			return runExport(cmd.Context(), r, outDir, includeRedacted)
		},
	}
	cmd.Flags().StringVarP(&outDir, "out", "o", ".", "output directory")
	cmd.Flags().BoolVar(&includeRedacted, "with-secrets", true, "write .env.redacted alongside nvoi.yaml")
	return cmd
}

func runExport(ctx context.Context, r *rt, outDir string, includeRedacted bool) error {
	st, _, err := openStore(ctx, r.flags)
	if err != nil {
		return err
	}
	defer st.Close()

	proj, err := st.GetProject(ctx, r.flags.ProjectName)
	if err != nil {
		return err
	}
	cfg, err := proj.ParsedConfig()
	if err != nil {
		return err
	}
	yml, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("export: marshal config: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("export: mkdir %s: %w", outDir, err)
	}
	yamlPath := outDir + "/nvoi.yaml"
	if err := os.WriteFile(yamlPath, yml, 0o644); err != nil {
		return fmt.Errorf("export: write %s: %w", yamlPath, err)
	}
	r.log.Info(fmt.Sprintf("wrote %s", yamlPath))

	if includeRedacted {
		names, err := st.ListSecretNames(ctx, proj.ID)
		if err != nil {
			return err
		}
		sort.Strings(names)
		var sb strings.Builder
		sb.WriteString("# nvoi secret names — values redacted; populate before use\n")
		for _, n := range names {
			sb.WriteString(n)
			sb.WriteString("=\n")
		}
		envPath := outDir + "/.env.redacted"
		if err := os.WriteFile(envPath, []byte(sb.String()), 0o600); err != nil {
			return fmt.Errorf("export: write %s: %w", envPath, err)
		}
		r.log.Info(fmt.Sprintf("wrote %s (%d names)", envPath, len(names)))
	}
	return nil
}
