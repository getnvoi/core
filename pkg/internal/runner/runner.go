// Package runner is the thin wrapper around terraform-exec, driving an
// OpenTofu binary (CLI-compatible with terraform; MPL-2.0). One Runner
// per command invocation. No retries, no fallbacks — tofu's own
// behavior is the contract.
//
// Output discipline: tofu ALWAYS runs in -json mode internally.
// Plan/Apply/Destroy use the *JSON tfexec variants and stream events
// into rt.Log.TFStream(), where tfTransformer parses each JSON line,
// lifts @level/@message/@timestamp, and emits normalized log Events.
// The log layer's serializer (JSONL vs tabbed-text projection) is what
// the operator's `--json` flag selects — tofu's invocation doesn't
// change with the flag. One code path, two render modes.
//
// Init has no -json mode → its native output is silenced. Any other
// stray writes from tfexec (e.g. the older Output() leak) hit
// io.Discard via SetStdout/SetStderr.
package runner

import (
	"context"
	"fmt"
	"io"

	"github.com/hashicorp/terraform-exec/tfexec"

	"github.com/getnvoi/core/pkg/runtime"
)

type Runner struct {
	tf *tfexec.Terraform
	rt *runtime.Runtime
}

// New downloads/locates tofu and points it at rt.WorkDir.
// tfexec's ambient stdout/stderr are wired to io.Discard — the
// *JSON variants we always use take their writer explicitly, so
// the ambient pipes only catch stray output (init banner, the
// `tofu output -json` pretty dump, etc.) which we don't want
// corrupting our event stream.
func New(ctx context.Context, rt *runtime.Runtime) (*Runner, error) {
	bin, err := EnsureTofu(ctx, rt.CacheDir)
	if err != nil {
		return nil, err
	}
	tf, err := tfexec.NewTerraform(rt.WorkDir, bin)
	if err != nil {
		return nil, fmt.Errorf("tfexec init %s: %w", rt.WorkDir, err)
	}
	if err := tf.SetEnv(terraformEnv(rt)); err != nil {
		return nil, fmt.Errorf("tfexec env %s: %w", rt.WorkDir, err)
	}
	tf.SetStdout(io.Discard)
	tf.SetStderr(io.Discard)
	return &Runner{tf: tf, rt: rt}, nil
}

func terraformEnv(rt *runtime.Runtime) map[string]string {
	env := map[string]string{}
	if cf := rt.Providers.Cloudflare; cf != nil {
		if cf.APIToken != "" {
			env["CLOUDFLARE_API_TOKEN"] = cf.APIToken
		}
	}
	if hz := rt.Providers.Hetzner; hz != nil {
		if hz.Token != "" {
			env["HCLOUD_TOKEN"] = hz.Token
		}
	}
	return env
}

// Init runs `tofu init` with -force-copy so backend transitions
// (local↔remote, between buckets) auto-confirm without prompts.
// OpenTofu auto-migrates on backend change when -force-copy is
// set. No-op when no migration is needed. Init has no -json mode;
// its native output is silenced via the runner's SetStdout to
// io.Discard.
func (r *Runner) Init(ctx context.Context) error {
	return r.tf.Init(ctx,
		tfexec.Upgrade(false),
		tfexec.ForceCopy(true),
	)
}

// Plan runs a no-out plan and pipes terraform's -json events into the
// log layer's TFStream where tfTransformer normalizes them.
func (r *Runner) Plan(ctx context.Context) (bool, error) {
	return r.tf.PlanJSON(ctx, r.rt.Log.TFStream())
}

// Apply runs a no-plan apply (uses current state). Same -json piping.
func (r *Runner) Apply(ctx context.Context) error {
	return r.tf.ApplyJSON(ctx, r.rt.Log.TFStream())
}

// Destroy runs a no-plan destroy. Same -json piping.
func (r *Runner) Destroy(ctx context.Context) error {
	return r.tf.DestroyJSON(ctx, r.rt.Log.TFStream())
}
