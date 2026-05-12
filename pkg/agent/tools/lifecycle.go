package tools

import (
	"context"
	"encoding/json"
)

// One file for all three lifecycle verbs — they share the exact
// same exec + summarize shape. The only thing that varies is the
// verb name.

func init() {
	Register(Tool{
		Name: "nvoi_plan",
		Description: "Run `nvoi plan` to compute the tofu plan without applying. " +
			"Returns a structured summary (steps walked, errors, duration). " +
			"Always run plan before deploy when in doubt — non-destructive.",
		Schema:  jsonObject(map[string]any{}),
		Execute: lifecycleExecutor("plan"),
	})
	Register(Tool{
		Name: "nvoi_deploy",
		Description: "Run the full deploy pipeline (build + tf-apply + k3s install + " +
			"workloads). LONG-RUNNING (5+ minutes typical). Returns when finished " +
			"with a summary of the JSONL event stream. The model is blocked on this " +
			"tool call until completion — humans see live progress via the JSONL " +
			"on disk (--database mode) or via the verb's own --json stream.",
		Schema:  jsonObject(map[string]any{}),
		Execute: lifecycleExecutor("deploy"),
	})
	Register(Tool{
		Name: "nvoi_destroy",
		Description: "Tear down the entire deployment (workloads → cluster → infra). " +
			"DESTRUCTIVE. Confirm with the user before invoking.",
		Schema:  jsonObject(map[string]any{}),
		Execute: lifecycleExecutor("destroy"),
	})
}

// lifecycleExecutor returns an Execute func that runs `nvoi <verb> --json`
// and produces a deploySummary tool_result.
//
// stdoutCap is large (8 MB) because deploy emits dozens of JSONL events
// per second during workload apply; we want the full stream in memory
// so summarize can walk it. Stderr is capped tighter (128 KB) — used
// only for crash-before-JSONL diagnostics.
func lifecycleExecutor(verb string) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, _ json.RawMessage) (string, error) {
		args := buildNvoiArgs(ctx, verb, "--json")
		res, err := runNvoi(ctx, args, 8<<20 /* 8 MB */, 128<<10)
		if err != nil {
			return "", err
		}
		return summarizeDeployJSONL(res.Stdout, res.Stderr, res.ExitCode), nil
	}
}
