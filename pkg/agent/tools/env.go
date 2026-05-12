package tools

import (
	"context"
	"encoding/json"
	"strings"
)

func init() {
	Register(Tool{
		Name: "nvoi_env_check",
		Description: "Report which credentials are present vs missing for the current " +
			"nvoi config. Categories: infra, storage, secrets, registry. Run this " +
			"BEFORE plan/deploy/destroy when the operator might not have set " +
			"required credentials.",
		Schema:  jsonObject(map[string]any{}),
		Execute: execEnvCheck,
	})
}

func execEnvCheck(ctx context.Context, _ json.RawMessage) (string, error) {
	args := buildNvoiArgs(ctx, "env", "check", "--json")
	res, err := runNvoi(ctx, args, 64<<10, 16<<10)
	if err != nil {
		return "", err
	}
	// env check returns exit 2 on missing credentials (not 1). Both
	// the success body and the missing body are valid JSON on stdout
	// — pass either through to the model.
	switch res.ExitCode {
	case 0, 2:
		body := strings.TrimSpace(string(res.Stdout))
		if body == "" {
			return errResult("nvoi env check returned no output (stderr: %s)",
				strings.TrimSpace(string(res.Stderr))), nil
		}
		return body, nil
	default:
		return errResult("nvoi env check exit %d: %s",
			res.ExitCode, strings.TrimSpace(string(res.Stderr))), nil
	}
}
