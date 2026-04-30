// Package runner is the thin wrapper around terraform-exec. One Runner
// per command invocation. No retries, no fallbacks — terraform's own
// behavior is the contract, including its native output format.
//
// Output discipline: nvoi-tf does NOT reformat terraform's events.
// Terraform's text output flows through rt.Log.TFStream() (indented
// under our step markers in text mode); --json flips to terraform's
// -json mode and the raw stream passes through unchanged.
package runner

import (
	"context"
	"fmt"
	"io"

	"github.com/hashicorp/terraform-exec/tfexec"

	"github.com/getnvoi/core/internal/runtime"
)

type Runner struct {
	tf *tfexec.Terraform
	rt *runtime.Runtime
}

// New downloads/locates terraform and points it at rt.WorkDir. In text
// mode tfexec writes its native output into rt.Log.TFStream() (indented).
// In jsonl mode the *JSON variants stream raw -json events into the
// same writer; init has no -json flag so we silence it there.
func New(ctx context.Context, rt *runtime.Runtime) (*Runner, error) {
	bin, err := EnsureTerraform(ctx, rt.CacheDir)
	if err != nil {
		return nil, err
	}
	tf, err := tfexec.NewTerraform(rt.WorkDir, bin)
	if err != nil {
		return nil, fmt.Errorf("tfexec init %s: %w", rt.WorkDir, err)
	}
	if rt.Flags.JSON {
		// Init in JSON mode: silenced (terraform init has no -json mode).
		// Plan/Apply/Destroy use the *JSON variants which take the writer
		// as an argument.
		tf.SetStdout(io.Discard)
		tf.SetStderr(io.Discard)
	} else {
		// Text mode: terraform's native output, indented.
		tf.SetStdout(rt.Log.TFStream())
		tf.SetStderr(rt.Log.TFStream())
	}
	return &Runner{tf: tf, rt: rt}, nil
}

// Init runs `terraform init` with -force-copy so backend transitions
// (local↔remote, between buckets) auto-confirm without prompts.
// Terraform 1.9 auto-migrates on backend change when -force-copy is
// set. No-op when no migration is needed.
func (r *Runner) Init(ctx context.Context) error {
	return r.tf.Init(ctx,
		tfexec.Upgrade(false),
		tfexec.ForceCopy(true),
	)
}

// Plan returns (hasChanges, error). JSON mode pipes -json events into
// rt.Log.TFStream(); text mode uses tfexec's regular output handlers
// already wired in New().
func (r *Runner) Plan(ctx context.Context) (bool, error) {
	if r.rt.Flags.JSON {
		return r.tf.PlanJSON(ctx, r.rt.Log.TFStream())
	}
	return r.tf.Plan(ctx)
}

func (r *Runner) Apply(ctx context.Context) error {
	if r.rt.Flags.JSON {
		return r.tf.ApplyJSON(ctx, r.rt.Log.TFStream())
	}
	return r.tf.Apply(ctx)
}

func (r *Runner) Destroy(ctx context.Context) error {
	if r.rt.Flags.JSON {
		return r.tf.DestroyJSON(ctx, r.rt.Log.TFStream())
	}
	return r.tf.Destroy(ctx)
}
