package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/getnvoi/core/internal/compile"
	"github.com/getnvoi/core/internal/runner"
	"github.com/getnvoi/core/internal/runtime"
)

// WithRunner is the shared orchestration primitive: compile YAML →
// write bundle → build runner → run action. Used internally by Run /
// Destroy / Plan, AND by ad-hoc cmd/cli verbs (ssh, exec, logs,
// kubectl) that need a runner to read endpoints before dialing SSH.
//
// Bundle and Runner live as locals — they are produced here, consumed
// here, and never stashed on a struct.
func WithRunner(ctx context.Context, rt *runtime.Runtime, action func(context.Context, *runner.Runner) error) error {
	rt.Log.Step("compile")
	bundle, err := compile.Compile(rt)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	rt.Log.Step("write-bundle")
	if err := writeBundle(rt.WorkDir, bundle); err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}
	rt.Log.Step("tf-binary")
	run, err := runner.New(ctx, rt)
	if err != nil {
		return err
	}
	return action(ctx, run)
}

// writeBundle materializes the rendered HCL files under the work
// directory. Regenerated each run — the bundle is deterministic from
// the YAML, so stale files are overwritten safely.
func writeBundle(workDir string, b *compile.Bundle) error {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return err
	}
	files, err := b.Render()
	if err != nil {
		return err
	}
	for name, content := range files {
		path := filepath.Join(workDir, name)
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}
