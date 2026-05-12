package grafana

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/runtime"

	// Trigger provider registration for the BuildProvisioning paths
	// that resolve "postmark" and "twilio" via providers.ResolveEmail /
	// providers.ResolveSMS.
	_ "github.com/getnvoi/core/pkg/providers/postmark"
	_ "github.com/getnvoi/core/pkg/providers/twilio"
)

func TestBuildProvisioning_DatasourceAlwaysEmitted(t *testing.T) {
	rt := &runtime.Runtime{Cfg: &config.Config{}}
	b, err := BuildProvisioning(rt)
	if err != nil {
		t.Fatalf("BuildProvisioning: %v", err)
	}
	if len(b.ConfigMaps) < 1 || b.ConfigMaps[0] != DatasourceConfigMapName {
		t.Errorf("datasource CM should be the first declared, got %v", b.ConfigMaps)
	}
}

func TestBuildProvisioning_NoAlertsBlock_OnlyDatasource(t *testing.T) {
	rt := &runtime.Runtime{
		Cfg:     &config.Config{},
		Monitor: &runtime.ResolvedMonitor{}, // no alerts
	}
	b, err := BuildProvisioning(rt)
	if err != nil {
		t.Fatalf("BuildProvisioning: %v", err)
	}
	if len(b.ConfigMaps) != 1 {
		t.Errorf("no alerts → only datasource CM; got %v", b.ConfigMaps)
	}
	if len(b.Secrets) != 0 {
		t.Errorf("no alerts → no receiver Secrets; got %v", b.Secrets)
	}
}

func TestBuildProvisioning_SlackOnly(t *testing.T) {
	rt := &runtime.Runtime{
		Cfg: &config.Config{},
		Monitor: &runtime.ResolvedMonitor{
			Alerts: &runtime.ResolvedAlerts{Slack: "https://hooks.slack.com/abc"},
		},
	}
	b, err := BuildProvisioning(rt)
	if err != nil {
		t.Fatalf("BuildProvisioning: %v", err)
	}
	// Datasource + contact points + policy. No receiver Secrets
	// (slack doesn't have any).
	if len(b.ConfigMaps) != 3 {
		t.Errorf("slack-only → 3 CMs (datasource + contact-points + policy), got %d: %v", len(b.ConfigMaps), b.ConfigMaps)
	}
	if len(b.Secrets) != 0 {
		t.Errorf("slack-only → 0 Secrets, got %d", len(b.Secrets))
	}
}

func TestBuildProvisioning_FullChannels(t *testing.T) {
	rt := &runtime.Runtime{
		Cfg: &config.Config{},
		Monitor: &runtime.ResolvedMonitor{
			Alerts: &runtime.ResolvedAlerts{
				Slack: "https://hooks.slack.com/abc",
				Email: &providers.AlertSpec{
					Provider: "postmark",
					Fields: map[string]interface{}{
						"token": "tok",
						"from":  "a@b.c",
						"to":    []interface{}{"o@p.q"},
					},
				},
				SMS: &providers.AlertSpec{
					Provider: "twilio",
					Fields: map[string]interface{}{
						"account": "AC123",
						"token":   "secret",
						"from":    "+15558675309",
						"to":      []interface{}{"+33611223344"},
					},
				},
			},
		},
	}
	b, err := BuildProvisioning(rt)
	if err != nil {
		t.Fatalf("BuildProvisioning: %v", err)
	}
	// Datasource + contact-points + policy = 3 CMs.
	if len(b.ConfigMaps) != 3 {
		t.Errorf("want 3 CMs, got %d: %v", len(b.ConfigMaps), b.ConfigMaps)
	}
	// Postmark + Twilio each produce a Secret (postmark-creds, twilio-creds).
	if len(b.Secrets) != 2 {
		t.Errorf("want 2 Secrets, got %d: %v", len(b.Secrets), b.Secrets)
	}

	// Contact points CM should mention every channel.
	var cpYAML string
	for _, o := range b.Objects {
		if o.ConfigMap != nil && o.ConfigMap.Name == ContactsConfigMapName {
			cpYAML = o.ConfigMap.Data["contactpoints.yaml"]
		}
	}
	for _, want := range []string{"nvoi-slack", "nvoi-email", "nvoi-sms"} {
		if !strings.Contains(cpYAML, want) {
			t.Errorf("contactpoints.yaml missing %q\n%s", want, cpYAML)
		}
	}
}

func TestSlackReceiver_SettingsURL(t *testing.T) {
	r := SlackReceiver("https://x")
	if r.Type != "slack" || r.Settings["url"] != "https://x" {
		t.Errorf("slack receiver wrong: %+v", r)
	}
}

func TestFromReceiver_UnknownTypeErrors(t *testing.T) {
	_, err := FromReceiver(providers.Receiver{Type: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown receiver type")
	}
}
