package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

func init() {
	Register(Tool{
		Name: "nvoi_config_show",
		Description: "Get the parsed and validated nvoi config as a JSON object. " +
			"Read-only — does not modify any state.",
		Schema:  jsonObject(map[string]any{}),
		Execute: execConfigShow,
	})

	Register(Tool{
		Name: "nvoi_config_replace",
		Description: "Replace the entire nvoi config. Pass the full config as " +
			"a JSON object under `config`. Validates before writing — read the " +
			"error and fix the config before retrying. DESTRUCTIVE: overwrites " +
			"the existing config.",
		Schema: jsonObject(map[string]any{
			"config": map[string]any{
				"type":        "object",
				"description": "Full nvoi config object (matches the nvoi.yaml shape)",
			},
		}, "config"),
		Execute: execConfigReplace,
	})
}

func execConfigShow(ctx context.Context, _ json.RawMessage) (string, error) {
	args := buildNvoiArgs(ctx, "config", "show", "--json")
	res, err := runNvoi(ctx, args, 1<<20 /* 1 MB */, 32<<10)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return errResult("nvoi config show exit %d: %s",
			res.ExitCode, strings.TrimSpace(string(res.Stderr))), nil
	}
	// Stdout is one JSON object; pass through verbatim. The model
	// sees it as the tool_result body.
	return strings.TrimSpace(string(res.Stdout)), nil
}

func execConfigReplace(ctx context.Context, in json.RawMessage) (string, error) {
	var input struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal(in, &input); err != nil {
		return errResult("invalid tool input: %v", err), nil
	}
	if len(input.Config) == 0 {
		return errResult("config field required"), nil
	}
	args := buildNvoiArgs(ctx, "config", "replace", "--force")
	res, err := runNvoiStdin(ctx, args, input.Config, 32<<10, 32<<10)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return errResult("nvoi config replace exit %d: %s",
			res.ExitCode, strings.TrimSpace(string(res.Stderr))), nil
	}
	out, _ := json.Marshal(map[string]any{"ok": true})
	return string(out), nil
}

// buildNvoiArgs prepends the global flags every tool needs to thread
// to the child invocation: -c (yaml mode) OR --database/--keyring/
// --project (store mode), plus --json when the parent --json flag is
// set. Verb-specific args follow.
//
// Mutual exclusion is already enforced at the parent boundary; here
// we just forward whichever mode was active.
func buildNvoiArgs(ctx context.Context, verbAndArgs ...string) []string {
	env, _ := envFromCtx(ctx)
	out := []string{}
	if env.RootJSON {
		out = append(out, "--json")
	}
	if env.DatabasePath != "" {
		out = append(out, "--database", env.DatabasePath)
		if env.Keyring != "" {
			out = append(out, "--keyring", env.Keyring)
		}
		if env.ProjectName != "" {
			out = append(out, "--project", env.ProjectName)
		}
	} else if env.ConfigPath != "" {
		out = append(out, "-c", env.ConfigPath)
	}
	out = append(out, verbAndArgs...)
	return out
}

// Silence unused-fmt warning if Sprintf is dropped later.
var _ = fmt.Sprintf
