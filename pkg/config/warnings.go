package config

// warnings.go produces non-fatal advisory messages about the config.
// Pure (no env, no disk, no log) — returns strings; the cmd/cli
// boundary logs them via lg.Warn. Separate from Validate which only
// returns hard errors.
//
// Anything that should ABORT the deploy goes in validate.go; anything
// that's a "your config will work but you may regret it" goes here.

import "fmt"

// Warnings returns advisory messages about the config. Empty slice
// when nothing to flag.
func (c *Config) Warnings() []string {
	var out []string
	out = append(out, monitorSizingWarnings(c)...)
	return out
}

// monitorSizingWarnings flags the case where the operator activated
// monitor: on a cluster too small to comfortably host the stack.
// Stack budget is ~1Gi resident (see 00-overview.md table); a cax11
// is 4Gi total. Cohabiting with app workloads on a single cax11
// master is asking for OOM kills.
//
// Heuristic (Hetzner-specific):
//   - monitor: set AND
//   - no worker exists AND
//   - the only master is cax11-class (cax11, cx11)
//
// → warn. Operator may have a real reason (test cluster); we don't
// block.
//
// Cross-provider generalization is out of scope for v1. When a second
// provider lands with similar instance-size sensitivities, lift this
// into the provider package's per-type metadata.
func monitorSizingWarnings(c *Config) []string {
	if c.Monitor == nil {
		return nil
	}
	if !isHetznerSmallMasterOnly(c) {
		return nil
	}
	return []string{
		fmt.Sprintf("monitor: sizing — only master is small-class on hetzner (cax11/cx11). The observability stack budgets ~1Gi resident; tight on a 4Gi node sharing with app workloads. Consider a cax21+ master or adding a worker."),
	}
}

func isHetznerSmallMasterOnly(c *Config) bool {
	if c.Providers.Infra != "hetzner" {
		return false
	}
	masters := 0
	workers := 0
	var loneMasterType string
	for _, s := range c.Servers {
		switch s.Role {
		case "master":
			masters++
			loneMasterType = s.Type
		case "worker":
			workers++
		}
	}
	if workers > 0 || masters != 1 {
		return false
	}
	return loneMasterType == "cax11" || loneMasterType == "cx11"
}
