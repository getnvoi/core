package cli

import (
	"fmt"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/runtime"
)

// ResolveMonitor takes the raw cfg.Monitor and returns a
// *runtime.ResolvedMonitor with every $VAR substituted against
// `getenv`. Returns nil when cfg.Monitor is nil — matches the
// "monitor block absent → no observability stack" contract every
// downstream consumer relies on.
//
// Errors when a $VAR resolves to the empty string (same behavior as
// ResolveSecrets): a misconfigured .env is louder than a silent
// disabling of monitoring.
//
// Walks AlertSpec.Fields recursively — strings beginning with `$`
// resolve via utils.ResolveVar; nested lists keep their shape, with
// each string element resolved in place.
func ResolveMonitor(cfg *config.Config, getenv func(string) string) (*runtime.ResolvedMonitor, error) {
	if cfg == nil || cfg.Monitor == nil {
		return nil, nil
	}
	m := cfg.Monitor

	out := &runtime.ResolvedMonitor{
		Domain: m.Domain, // hostnames are literal — no $VAR substitution
	}
	if m.AdminPassword != "" {
		v, err := resolveStringRef("monitor.admin_password", m.AdminPassword, getenv)
		if err != nil {
			return nil, err
		}
		out.AdminPassword = v
	}

	if m.Alerts != nil {
		ra := &runtime.ResolvedAlerts{}
		if m.Alerts.Slack != "" {
			v, err := resolveStringRef("monitor.alerts.slack", m.Alerts.Slack, getenv)
			if err != nil {
				return nil, err
			}
			ra.Slack = v
		}
		if m.Alerts.Email != nil {
			rs, err := resolveAlertSpec("monitor.alerts.email", m.Alerts.Email, getenv)
			if err != nil {
				return nil, err
			}
			ra.Email = rs
		}
		if m.Alerts.SMS != nil {
			rs, err := resolveAlertSpec("monitor.alerts.sms", m.Alerts.SMS, getenv)
			if err != nil {
				return nil, err
			}
			ra.SMS = rs
		}
		out.Alerts = ra
	}
	return out, nil
}

// resolveStringRef substitutes a `$VAR` reference against getenv.
// When s is a $VAR ref and the env var is missing/empty, returns an
// error naming the path so operators can locate the broken reference
// in their YAML. Anything not starting with $ passes through verbatim.
//
// Mirrors pkg/internal/utils.ResolveVar but inlined here because
// pkg/internal/ is library-private and internal/cli/ cannot import it.
// The logic is 3 lines; duplication beats reshuffling the Go internal/
// boundary.
func resolveStringRef(path, s string, getenv func(string) string) (string, error) {
	if len(s) > 1 && s[0] == '$' {
		v := getenv(s[1:])
		if v == "" {
			return "", fmt.Errorf("%s: env var %s is unset or empty", path, s[1:])
		}
		return v, nil
	}
	return s, nil
}

// resolveAlertSpec deep-copies the AlertSpec with every string value
// in Fields substituted. The Provider name is copied verbatim — it's
// a registered factory name, never a $VAR.
//
// Recursive on map / list values so an inline list of $VAR strings
// (e.g. `to: [$ONCALL, $BACKUP]`) substitutes element-wise. Anything
// not a string passes through unchanged (lets future numeric / bool
// fields work without revisiting this code).
func resolveAlertSpec(path string, spec *providers.AlertSpec, getenv func(string) string) (*providers.AlertSpec, error) {
	if spec == nil {
		return nil, nil
	}
	resolved := &providers.AlertSpec{
		Provider: spec.Provider,
		Fields:   make(map[string]interface{}, len(spec.Fields)),
	}
	for k, v := range spec.Fields {
		rv, err := resolveAny(fmt.Sprintf("%s.%s", path, k), v, getenv)
		if err != nil {
			return nil, err
		}
		resolved.Fields[k] = rv
	}
	return resolved, nil
}

// resolveAny walks one node of the Fields tree. Strings beginning
// with `$` substitute. Lists recurse element-wise. Everything else
// passes through unchanged.
func resolveAny(path string, v interface{}, getenv func(string) string) (interface{}, error) {
	switch t := v.(type) {
	case string:
		return resolveStringRef(path, t, getenv)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, elem := range t {
			rv, err := resolveAny(fmt.Sprintf("%s[%d]", path, i), elem, getenv)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}
