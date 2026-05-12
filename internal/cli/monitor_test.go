package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cli "github.com/getnvoi/core/internal/cli"
	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/providers"
)

// fakeGetenv returns a closure over a fixed env map for deterministic
// resolution. Matches the getenv func(string) string signature
// ResolveMonitor expects.
func fakeGetenv(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

func TestResolveMonitor_Nil(t *testing.T) {
	got, err := cli.ResolveMonitor(nil, fakeGetenv(nil), "")
	if err != nil {
		t.Fatalf("nil cfg: unexpected error %v", err)
	}
	if got != nil {
		t.Errorf("nil cfg should yield nil monitor, got %+v", got)
	}

	cfg := &config.Config{} // cfg.Monitor == nil
	got, err = cli.ResolveMonitor(cfg, fakeGetenv(nil), "")
	if err != nil {
		t.Fatalf("nil monitor: unexpected error %v", err)
	}
	if got != nil {
		t.Errorf("nil monitor should yield nil resolved, got %+v", got)
	}
}

func TestResolveMonitor_Empty(t *testing.T) {
	cfg := &config.Config{Monitor: &config.MonitorSpec{}}
	got, err := cli.ResolveMonitor(cfg, fakeGetenv(nil), "")
	if err != nil {
		t.Fatalf("empty monitor: %v", err)
	}
	if got == nil {
		t.Fatal("empty monitor should yield non-nil resolved")
	}
	if got.Domain != "" || got.AdminPassword != "" || got.Alerts != nil {
		t.Errorf("empty monitor should resolve to zero-value: %+v", got)
	}
}

func TestResolveMonitor_ResolvesAdminPassword(t *testing.T) {
	cfg := &config.Config{
		Monitor: &config.MonitorSpec{
			Domain:        "grafana.nvoi.to",
			AdminPassword: "$GRAFANA_PWD",
		},
	}
	got, err := cli.ResolveMonitor(cfg, fakeGetenv(map[string]string{"GRAFANA_PWD": "s3cr3t"}), "")
	if err != nil {
		t.Fatalf("ResolveMonitor: %v", err)
	}
	if got.Domain != "grafana.nvoi.to" {
		t.Errorf("Domain mutated: %q", got.Domain)
	}
	if got.AdminPassword != "s3cr3t" {
		t.Errorf("AdminPassword not resolved: %q", got.AdminPassword)
	}
}

func TestResolveMonitor_LiteralAdminPasswordPassesThrough(t *testing.T) {
	cfg := &config.Config{
		Monitor: &config.MonitorSpec{
			Domain:        "grafana.nvoi.to",
			AdminPassword: "literal-pwd",
		},
	}
	got, err := cli.ResolveMonitor(cfg, fakeGetenv(nil), "")
	if err != nil {
		t.Fatalf("ResolveMonitor: %v", err)
	}
	if got.AdminPassword != "literal-pwd" {
		t.Errorf("literal should pass through, got %q", got.AdminPassword)
	}
}

func TestResolveMonitor_MissingEnvVarErrors(t *testing.T) {
	cfg := &config.Config{
		Monitor: &config.MonitorSpec{AdminPassword: "$MISSING"},
	}
	_, err := cli.ResolveMonitor(cfg, fakeGetenv(nil), "")
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
	if !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("error should name the missing var: %v", err)
	}
	if !strings.Contains(err.Error(), "admin_password") {
		t.Errorf("error should name the YAML path: %v", err)
	}
}

func TestResolveMonitor_ResolvesAlertsSlack(t *testing.T) {
	cfg := &config.Config{
		Monitor: &config.MonitorSpec{
			Alerts: &config.AlertsSpec{Slack: "$SLACK_URL"},
		},
	}
	got, err := cli.ResolveMonitor(cfg, fakeGetenv(map[string]string{"SLACK_URL": "https://hooks.slack.com/x"}), "")
	if err != nil {
		t.Fatalf("ResolveMonitor: %v", err)
	}
	if got.Alerts == nil || got.Alerts.Slack != "https://hooks.slack.com/x" {
		t.Errorf("Slack not resolved: %+v", got.Alerts)
	}
}

func TestResolveMonitor_ResolvesAlertSpecFields(t *testing.T) {
	cfg := &config.Config{
		Monitor: &config.MonitorSpec{
			Alerts: &config.AlertsSpec{
				Email: &providers.AlertSpec{
					Provider: "postmark",
					Fields: map[string]interface{}{
						"token": "$POSTMARK_TOKEN",
						"from":  "alerts@nvoi.to",
						"to":    []interface{}{"$PRIMARY_OPS", "secondary@nvoi.to"},
					},
				},
			},
		},
	}
	got, err := cli.ResolveMonitor(cfg, fakeGetenv(map[string]string{
		"POSTMARK_TOKEN": "server-token",
		"PRIMARY_OPS":    "ops@nvoi.to",
	}), "")
	if err != nil {
		t.Fatalf("ResolveMonitor: %v", err)
	}
	email := got.Alerts.Email
	if email == nil {
		t.Fatal("Email not resolved")
	}
	if email.Provider != "postmark" {
		t.Errorf("Provider mutated: %q", email.Provider)
	}
	if email.Fields["token"] != "server-token" {
		t.Errorf("token not resolved: %v", email.Fields["token"])
	}
	if email.Fields["from"] != "alerts@nvoi.to" {
		t.Errorf("literal mutated: %v", email.Fields["from"])
	}
	to, ok := email.Fields["to"].([]interface{})
	if !ok {
		t.Fatalf("to wrong type: %T", email.Fields["to"])
	}
	if len(to) != 2 || to[0] != "ops@nvoi.to" || to[1] != "secondary@nvoi.to" {
		t.Errorf("to list wrong: %v", to)
	}
}

func TestResolveMonitor_ProviderNamePreserved(t *testing.T) {
	cfg := &config.Config{
		Monitor: &config.MonitorSpec{
			Alerts: &config.AlertsSpec{
				SMS: &providers.AlertSpec{Provider: "twilio", Fields: map[string]interface{}{}},
			},
		},
	}
	got, err := cli.ResolveMonitor(cfg, fakeGetenv(nil), "")
	if err != nil {
		t.Fatalf("ResolveMonitor: %v", err)
	}
	if got.Alerts.SMS.Provider != "twilio" {
		t.Errorf("Provider should pass through: %q", got.Alerts.SMS.Provider)
	}
}

func TestResolveMonitor_MissingFieldEnvVarErrors(t *testing.T) {
	cfg := &config.Config{
		Monitor: &config.MonitorSpec{
			Alerts: &config.AlertsSpec{
				SMS: &providers.AlertSpec{
					Provider: "twilio",
					Fields:   map[string]interface{}{"account": "$MISSING_TWILIO"},
				},
			},
		},
	}
	_, err := cli.ResolveMonitor(cfg, fakeGetenv(nil), "")
	if err == nil {
		t.Fatal("expected error for missing env var in AlertSpec field")
	}
	if !strings.Contains(err.Error(), "monitor.alerts.sms.account") {
		t.Errorf("error should name the YAML path: %v", err)
	}
}

// ── monitor.dashboards / monitor.alert_rules file loading ────────────

func TestResolveMonitor_LoadsDashboardFiles(t *testing.T) {
	dir := t.TempDir()
	must := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	must("a.json", `{"title":"A"}`)
	must("b.json", `{"title":"B"}`)
	must("ignored.txt", "not-json")

	cfg := &config.Config{
		Monitor: &config.MonitorSpec{
			Dashboards: []string{"*.json"},
		},
	}
	got, err := cli.ResolveMonitor(cfg, fakeGetenv(nil), dir)
	if err != nil {
		t.Fatalf("ResolveMonitor: %v", err)
	}
	if len(got.Dashboards) != 2 {
		t.Fatalf("want 2 dashboards, got %d", len(got.Dashboards))
	}
	names := []string{got.Dashboards[0].Name, got.Dashboards[1].Name}
	for _, want := range []string{"a.json", "b.json"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing %q in %v", want, names)
		}
	}
}

func TestResolveMonitor_EmptyDashboards(t *testing.T) {
	cfg := &config.Config{Monitor: &config.MonitorSpec{}}
	got, err := cli.ResolveMonitor(cfg, fakeGetenv(nil), "")
	if err != nil {
		t.Fatalf("ResolveMonitor: %v", err)
	}
	if len(got.Dashboards) != 0 {
		t.Errorf("empty monitor.dashboards should yield 0 files, got %d", len(got.Dashboards))
	}
}

func TestResolveMonitor_GlobNoMatchErrors(t *testing.T) {
	cfg := &config.Config{
		Monitor: &config.MonitorSpec{
			Dashboards: []string{"nonexistent/*.json"},
		},
	}
	_, err := cli.ResolveMonitor(cfg, fakeGetenv(nil), t.TempDir())
	if err == nil {
		t.Fatal("expected error for glob matching no files")
	}
	if !strings.Contains(err.Error(), "no files match") {
		t.Errorf("error should mention no matches: %v", err)
	}
}

func TestResolveMonitor_DuplicateBasenameErrors(t *testing.T) {
	d1 := t.TempDir()
	d2 := t.TempDir()
	for _, dir := range []string{d1, d2} {
		if err := os.WriteFile(filepath.Join(dir, "same.json"), []byte("{}"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	cfg := &config.Config{
		Monitor: &config.MonitorSpec{
			Dashboards: []string{filepath.Join(d1, "*.json"), filepath.Join(d2, "*.json")},
		},
	}
	_, err := cli.ResolveMonitor(cfg, fakeGetenv(nil), "")
	if err == nil {
		t.Fatal("expected error for duplicate basename")
	}
	if !strings.Contains(err.Error(), "duplicate basename") {
		t.Errorf("error should flag duplicate: %v", err)
	}
}
