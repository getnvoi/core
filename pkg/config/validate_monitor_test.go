package config_test

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/providers"

	// Triggers registration of:
	//   - cloudflare (BucketProvider + DNS emitter) so providers.storage
	//     and providers.dns validation resolves "cloudflare".
	//   - hetzner (InfraEmitter + reserved-names) so providers.infra
	//     validation resolves "hetzner".
	//   - postmark (EmailProvider) so monitor.alerts.email.provider
	//     validation resolves "postmark".
	//   - twilio (SMSProvider) so monitor.alerts.sms.provider
	//     validation resolves "twilio".
	_ "github.com/getnvoi/core/pkg/providers/cloudflare"
	_ "github.com/getnvoi/core/pkg/providers/hetzner"
	_ "github.com/getnvoi/core/pkg/providers/postmark"
	_ "github.com/getnvoi/core/pkg/providers/twilio"
)

// baseMonitorConfig is the minimal valid skeleton every monitor-test
// mutates from. Providers.storage = cloudflare satisfies the
// monitor:-requires-storage rule for the "no monitor" tests too.
func baseMonitorConfig() *config.Config {
	return &config.Config{
		App: "hello",
		Env: "dev",
		Providers: config.Providers{
			Infra:   "hetzner",
			Storage: "cloudflare",
			DNS:     "cloudflare",
		},
		SSHKey: "/tmp/x.pub",
		Servers: map[string]config.ServerSpec{
			"master": {Type: "cax11", Region: "nbg1", Role: "master"},
		},
	}
}

func TestValidate_Monitor_MinimalValid(t *testing.T) {
	c := baseMonitorConfig()
	c.Monitor = &config.MonitorSpec{} // empty monitor: {} is valid
	if err := c.Validate(); err != nil {
		t.Fatalf("monitor: {} should validate with storage+dns set, got %v", err)
	}
}

func TestValidate_Monitor_RequiresStorage(t *testing.T) {
	c := baseMonitorConfig()
	c.Providers.Storage = ""
	c.Monitor = &config.MonitorSpec{}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: monitor without providers.storage")
	}
	if !strings.Contains(err.Error(), "monitor: requires providers.storage") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Monitor_DomainRequiresDNS(t *testing.T) {
	c := baseMonitorConfig()
	c.Providers.DNS = ""
	c.Monitor = &config.MonitorSpec{
		Domain:        "grafana.nvoi.to",
		AdminPassword: "$X",
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: monitor.domain without providers.dns")
	}
	if !strings.Contains(err.Error(), "monitor.domain: requires providers.dns") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Monitor_DomainRequiresAdminPassword(t *testing.T) {
	c := baseMonitorConfig()
	c.Monitor = &config.MonitorSpec{Domain: "grafana.nvoi.to"}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: monitor.domain without admin_password")
	}
	if !strings.Contains(err.Error(), "monitor.admin_password: required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Monitor_DomainMustBeValidHostname(t *testing.T) {
	c := baseMonitorConfig()
	c.Monitor = &config.MonitorSpec{
		Domain:        "Not a Hostname!",
		AdminPassword: "$X",
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: invalid hostname")
	}
	if !strings.Contains(err.Error(), "monitor.domain") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Monitor_EmailProvider_Registered(t *testing.T) {
	if !providers.IsRegisteredEmail("postmark") {
		t.Fatal("postmark not registered — blank-import broken?")
	}
	c := baseMonitorConfig()
	c.Monitor = &config.MonitorSpec{
		Alerts: &config.AlertsSpec{
			Email: &providers.AlertSpec{Provider: "postmark"},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("registered email provider should validate, got %v", err)
	}
}

func TestValidate_Monitor_EmailProvider_Unknown(t *testing.T) {
	c := baseMonitorConfig()
	c.Monitor = &config.MonitorSpec{
		Alerts: &config.AlertsSpec{
			Email: &providers.AlertSpec{Provider: "nonexistent"},
		},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: unknown email provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Monitor_EmailProvider_Empty(t *testing.T) {
	c := baseMonitorConfig()
	c.Monitor = &config.MonitorSpec{
		Alerts: &config.AlertsSpec{
			Email: &providers.AlertSpec{Provider: ""},
		},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: empty email provider")
	}
	if !strings.Contains(err.Error(), "monitor.alerts.email.provider: required") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Monitor_SMSProvider_Registered(t *testing.T) {
	if !providers.IsRegisteredSMS("twilio") {
		t.Fatal("twilio not registered — blank-import broken?")
	}
	c := baseMonitorConfig()
	c.Monitor = &config.MonitorSpec{
		Alerts: &config.AlertsSpec{
			SMS: &providers.AlertSpec{Provider: "twilio"},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("registered sms provider should validate, got %v", err)
	}
}

func TestValidate_Monitor_SMSProvider_Unknown(t *testing.T) {
	c := baseMonitorConfig()
	c.Monitor = &config.MonitorSpec{
		Alerts: &config.AlertsSpec{
			SMS: &providers.AlertSpec{Provider: "nonexistent-sms"},
		},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error: unknown sms provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_Monitor_FullSpec(t *testing.T) {
	c := baseMonitorConfig()
	c.Monitor = &config.MonitorSpec{
		Domain:        "grafana.nvoi.to",
		AdminPassword: "$GRAFANA_ADMIN_PWD",
		Alerts: &config.AlertsSpec{
			Slack: "$SLACK_URL",
			Email: &providers.AlertSpec{
				Provider: "postmark",
				Fields: map[string]interface{}{
					"token": "$POSTMARK_TOKEN",
					"from":  "alerts@nvoi.to",
					"to":    []interface{}{"ops@nvoi.to"},
				},
			},
			SMS: &providers.AlertSpec{
				Provider: "twilio",
				Fields: map[string]interface{}{
					"account": "$TWILIO_ACCOUNT",
					"token":   "$TWILIO_TOKEN",
					"from":    "$TWILIO_FROM",
					"to":      []interface{}{"+33611223344"},
				},
			},
		},
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("full monitor spec should validate, got %v", err)
	}
}
