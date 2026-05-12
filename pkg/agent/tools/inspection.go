package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// One file for the four inspection tools (logs, exec, kubectl, ssh).
// They all wrap a verb that produces plain text (not JSONL) and return
// captured stdout/stderr as the tool_result.

func init() {
	Register(Tool{
		Name: "nvoi_logs",
		Description: "Fetch pod logs for a service via kubectl logs on the master. " +
			"Synchronous (no --follow); for live tailing the user runs the CLI " +
			"directly. Specify `tail` to bound the line count.",
		Schema: jsonObject(map[string]any{
			"service": stringProp("Service name from the nvoi config"),
			"tail":    intProp("Return only the last N lines (default 200)"),
			"since":   stringProp("Go duration: 5m, 1h, 24h (optional)"),
		}, "service"),
		Execute: execLogs,
	})
	Register(Tool{
		Name: "nvoi_exec",
		Description: "Run a command in a service's pod via kubectl exec. The command " +
			"is passed as an array of strings (NOT joined as a shell string).",
		Schema: jsonObject(map[string]any{
			"service": stringProp("Service name from the nvoi config"),
			"command": arrayProp("string", "argv to exec inside the pod"),
		}, "service", "command"),
		Execute: execExec,
	})
	Register(Tool{
		Name: "nvoi_kubectl",
		Description: "Run a kubectl command against the cluster (via the master SSH " +
			"tunnel). Args are passed as an array of strings.",
		Schema: jsonObject(map[string]any{
			"args": arrayProp("string", "kubectl argv"),
		}, "args"),
		Execute: execKubectl,
	})
	Register(Tool{
		Name: "nvoi_ssh",
		Description: "Run a command on a cluster node via SSH. Target is the node " +
			"name from the nvoi config (e.g. 'master').",
		Schema: jsonObject(map[string]any{
			"target":  stringProp("Server name from the nvoi config"),
			"command": arrayProp("string", "argv to run on the remote shell"),
		}, "target", "command"),
		Execute: execSSH,
	})
}

func execLogs(ctx context.Context, in json.RawMessage) (string, error) {
	var input struct {
		Service string `json:"service"`
		Tail    int    `json:"tail"`
		Since   string `json:"since"`
	}
	if err := json.Unmarshal(in, &input); err != nil {
		return errResult("invalid tool input: %v", err), nil
	}
	if input.Service == "" {
		return errResult("service required"), nil
	}
	if input.Tail <= 0 {
		input.Tail = 200
	}
	args := buildNvoiArgs(ctx, "logs", input.Service, fmt.Sprintf("--tail=%d", input.Tail))
	if input.Since != "" {
		args = append(args, "--since="+input.Since)
	}
	res, err := runNvoi(ctx, args, 512<<10, 32<<10)
	if err != nil {
		return "", err
	}
	return wrapTextOutput(res), nil
}

func execExec(ctx context.Context, in json.RawMessage) (string, error) {
	var input struct {
		Service string   `json:"service"`
		Command []string `json:"command"`
	}
	if err := json.Unmarshal(in, &input); err != nil {
		return errResult("invalid tool input: %v", err), nil
	}
	if input.Service == "" {
		return errResult("service required"), nil
	}
	if len(input.Command) == 0 {
		return errResult("command must be non-empty array of strings"), nil
	}
	args := buildNvoiArgs(ctx, "exec", input.Service, "--")
	args = append(args, input.Command...)
	res, err := runNvoi(ctx, args, 512<<10, 32<<10)
	if err != nil {
		return "", err
	}
	return wrapTextOutput(res), nil
}

func execKubectl(ctx context.Context, in json.RawMessage) (string, error) {
	var input struct {
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(in, &input); err != nil {
		return errResult("invalid tool input: %v", err), nil
	}
	if len(input.Args) == 0 {
		return errResult("args must be non-empty array of strings"), nil
	}
	args := buildNvoiArgs(ctx, "kubectl", "--")
	args = append(args, input.Args...)
	res, err := runNvoi(ctx, args, 512<<10, 32<<10)
	if err != nil {
		return "", err
	}
	return wrapTextOutput(res), nil
}

func execSSH(ctx context.Context, in json.RawMessage) (string, error) {
	var input struct {
		Target  string   `json:"target"`
		Command []string `json:"command"`
	}
	if err := json.Unmarshal(in, &input); err != nil {
		return errResult("invalid tool input: %v", err), nil
	}
	if input.Target == "" {
		return errResult("target required"), nil
	}
	if len(input.Command) == 0 {
		return errResult("command must be non-empty array of strings"), nil
	}
	args := buildNvoiArgs(ctx, "ssh", input.Target, "--")
	args = append(args, input.Command...)
	res, err := runNvoi(ctx, args, 512<<10, 32<<10)
	if err != nil {
		return "", err
	}
	return wrapTextOutput(res), nil
}

// wrapTextOutput is the shared shape for tools that emit plain text
// (logs / exec / kubectl / ssh) rather than JSONL. Returns a JSON
// object with stdout / stderr / exit_code so the model can read all
// three.
func wrapTextOutput(res runResult) string {
	body, _ := json.Marshal(map[string]any{
		"exit_code": res.ExitCode,
		"stdout":    strings.TrimRight(string(res.Stdout), "\n"),
		"stderr":    strings.TrimRight(string(res.Stderr), "\n"),
	})
	return string(body)
}
