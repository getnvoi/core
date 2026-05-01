package deploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/getnvoi/core/pkg/compile"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/runner"
	"github.com/getnvoi/core/pkg/runtime"
)

// WithRunner is the shared orchestration primitive: compile YAML →
// write bundle → build runner → run action. Used internally by Run /
// Destroy / Plan, AND by ad-hoc cmd/cli verbs (ssh, exec, logs,
// kubectl) that need a runner to read endpoints before dialing SSH.
//
// Bundle and Runner live as locals — they are produced here, consumed
// here, and never stashed on a struct.
//
// Steps emitted here (compile / write-bundle / tf-binary) all carry
// kind=infra — they're the operator-side preparation phase before
// anything cluster- or build-side runs.
func WithRunner(ctx context.Context, rt *runtime.Runtime, action func(context.Context, *runner.Runner) error) error {
	lg := rt.Log.Sub(log.KindInfra)
	lg.Step("compile")
	bundle, err := compile.Compile(rt)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	lg.Step("write-bundle")
	if err := writeBundle(rt.WorkDir, bundle); err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}
	lg.Step("tf-binary")
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
