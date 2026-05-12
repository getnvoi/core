package grafana

// provisioning.go composes datasources + contact points + notification
// policy into the ConfigMap+Secret bundle that PR 6's deploy phase
// applies. Single entry: BuildProvisioning(rt). Slack / Email / SMS
// channels translate to contact points; SecretRefs collect into a
// per-receiver Secret in nvoi-observability.

import (
	"bytes"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"gopkg.in/yaml.v3"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/runtime"
)

const (
	// AlertingNamespace is duplicated here (alongside
	// pkg/internal/observability.Namespace) so this subpackage stays
	// importable without dragging the parent in. Kept in sync via the
	// matching test.
	alertingNamespace = "nvoi-observability"

	// ContactsConfigMapName holds the contact-points YAML mounted at
	// /etc/grafana/provisioning/alerting/contactpoints.yaml.
	ContactsConfigMapName = "grafana-contact-points"

	// PolicyConfigMapName holds the notification-policy YAML mounted
	// at /etc/grafana/provisioning/alerting/policies.yaml.
	PolicyConfigMapName = "grafana-notification-policy"
)

// ProvisioningBundle is what BuildProvisioning returns: the typed
// objects (ConfigMaps + per-receiver Secrets) plus the per-Kind name
// lists the deploy phase hands to SweepOwned.
type ProvisioningBundle struct {
	Objects     []provisioningObject // ordered apply list (Secrets first, then ConfigMaps)
	ConfigMaps  []string             // declared names — for sweep
	Secrets     []string
}

// provisioningObject is a typed pointer either to a Secret or to a
// ConfigMap. The deploy phase iterates and calls kc.ApplyOwned;
// keeping it lightly typed lets us iterate without reflection.
type provisioningObject struct {
	Secret    *corev1.Secret
	ConfigMap *corev1.ConfigMap
}

// BuildProvisioning materializes datasources + receivers + policy
// from rt.Monitor. Receivers come from registered providers via
// ResolveSMS / ResolveEmail; Slack is a direct field. Returns the
// bundle ready for kc.ApplyOwned.
//
// When rt.Monitor.Alerts is nil → no contact points / policy emitted
// (alerts fire to the Grafana dashboard only). The datasource
// ConfigMap is always emitted.
func BuildProvisioning(rt *runtime.Runtime) (ProvisioningBundle, error) {
	var bundle ProvisioningBundle

	// Datasource ConfigMap — always present (alerts use it too).
	ds := BuildDatasourceConfigMap()
	bundle.Objects = append(bundle.Objects, provisioningObject{ConfigMap: ds})
	bundle.ConfigMaps = append(bundle.ConfigMaps, ds.Name)

	if rt.Monitor == nil || rt.Monitor.Alerts == nil {
		return bundle, nil
	}

	// Collect Receivers from every configured channel.
	receivers, err := buildReceivers(rt)
	if err != nil {
		return bundle, fmt.Errorf("build receivers: %w", err)
	}

	// Bail when no channel is configured AT ALL (no Slack URL + no
	// receivers). Slack alone is sufficient to emit contact points
	// even though it produces no Receiver (no SecretRefs).
	if len(receivers) == 0 && rt.Monitor.Alerts.Slack == "" {
		return bundle, nil
	}

	// Materialize SecretRefs into Kubernetes Secrets — one per
	// distinct SecretRef.Name, with all keys merged.
	for _, sec := range materializeSecrets(receivers) {
		bundle.Objects = append(bundle.Objects, provisioningObject{Secret: sec})
		bundle.Secrets = append(bundle.Secrets, sec.Name)
	}

	// Translate Receivers to Grafana ContactPointSpecs.
	contactPoints, err := translateContactPoints(rt.Monitor.Alerts.Slack, receivers)
	if err != nil {
		return bundle, fmt.Errorf("translate contact points: %w", err)
	}

	// Build the multi-integration grouping contact-points YAML
	// + the notification policy ConfigMap.
	cpCM, err := buildContactPointsConfigMap(contactPoints)
	if err != nil {
		return bundle, err
	}
	bundle.Objects = append(bundle.Objects, provisioningObject{ConfigMap: cpCM})
	bundle.ConfigMaps = append(bundle.ConfigMaps, cpCM.Name)

	npCM := buildNotificationPolicyConfigMap()
	bundle.Objects = append(bundle.Objects, provisioningObject{ConfigMap: npCM})
	bundle.ConfigMaps = append(bundle.ConfigMaps, npCM.Name)

	return bundle, nil
}

// buildReceivers calls each configured provider's BuildReceiver and
// returns the resulting Receivers in deterministic order.
func buildReceivers(rt *runtime.Runtime) ([]providers.Receiver, error) {
	var out []providers.Receiver
	alerts := rt.Monitor.Alerts

	if alerts.Email != nil {
		p, err := providers.ResolveEmail(alerts.Email.Provider)
		if err != nil {
			return nil, fmt.Errorf("email provider: %w", err)
		}
		r, err := p.BuildReceiver(*alerts.Email)
		if err != nil {
			return nil, fmt.Errorf("email BuildReceiver: %w", err)
		}
		out = append(out, r)
	}
	if alerts.SMS != nil {
		p, err := providers.ResolveSMS(alerts.SMS.Provider)
		if err != nil {
			return nil, fmt.Errorf("sms provider: %w", err)
		}
		r, err := p.BuildReceiver(*alerts.SMS)
		if err != nil {
			return nil, fmt.Errorf("sms BuildReceiver: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// materializeSecrets groups SecretRefs by Secret name and emits one
// typed Secret per name with all keys merged. Operators reading
// `kubectl get secret -n nvoi-observability -L nvoi/owner` see one
// Secret per registered provider (twilio-creds, postmark-creds, …)
// rather than one Secret per credential field.
func materializeSecrets(receivers []providers.Receiver) []*corev1.Secret {
	byName := map[string]map[string]string{}
	for _, r := range receivers {
		for _, s := range r.Secrets {
			if byName[s.Name] == nil {
				byName[s.Name] = map[string]string{}
			}
			byName[s.Name][s.Key] = s.From
		}
	}
	out := make([]*corev1.Secret, 0, len(byName))
	for name, data := range byName {
		out = append(out, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: alertingNamespace,
				Labels: map[string]string{
					"app.kubernetes.io/name": "grafana",
					kube.LabelOwner:          kube.OwnerObservability,
				},
			},
			Type:       corev1.SecretTypeOpaque,
			StringData: data,
		})
	}
	return out
}

// translateContactPoints walks providers.Receiver(s) + the operator's
// raw Slack URL and returns ContactPointSpecs covering every
// configured channel. Errors surface unknown Receiver.Type values
// (a programmer error in nvoi, not an operator error).
func translateContactPoints(slackURL string, receivers []providers.Receiver) ([]ContactPointSpec, error) {
	var out []ContactPointSpec
	if slackURL != "" {
		out = append(out, SlackReceiver(slackURL))
	}
	for _, r := range receivers {
		cp, err := FromReceiver(r)
		if err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, nil
}

// buildContactPointsConfigMap renders the contact-points provisioning
// YAML. Single ConfigMap; multiple ContactPointSpec entries under a
// single "nvoi-default" receiver. Grafana's policy routes all alerts
// through that receiver, which in turn fans out to every integration.
func buildContactPointsConfigMap(cps []ContactPointSpec) (*corev1.ConfigMap, error) {
	yml, err := renderContactPointsYAML(cps)
	if err != nil {
		return nil, err
	}
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ContactsConfigMapName,
			Namespace: alertingNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "grafana",
				kube.LabelOwner:          kube.OwnerObservability,
			},
		},
		Data: map[string]string{"contactpoints.yaml": yml},
	}, nil
}

// renderContactPointsYAML emits provisioning v1 YAML. Hand-built
// (not gopkg.in/yaml.v3 over a typed struct) because Grafana's
// provisioning shape has a quirky multi-integration receiver that
// the typed struct would obscure.
func renderContactPointsYAML(cps []ContactPointSpec) (string, error) {
	var buf bytes.Buffer
	buf.WriteString("apiVersion: 1\ncontactPoints:\n")
	buf.WriteString("  - orgId: 1\n")
	buf.WriteString("    name: nvoi-default\n")
	buf.WriteString("    receivers:\n")
	for _, cp := range cps {
		yml, err := yaml.Marshal(cp)
		if err != nil {
			return "", fmt.Errorf("marshal contact point %q: %w", cp.Name, err)
		}
		// Indent every line under `- ` so it fits in the receivers list.
		lines := strings.Split(strings.TrimRight(string(yml), "\n"), "\n")
		for i, ln := range lines {
			if i == 0 {
				buf.WriteString("      - " + ln + "\n")
			} else {
				buf.WriteString("        " + ln + "\n")
			}
		}
	}
	return buf.String(), nil
}

// buildNotificationPolicyConfigMap wraps the static policy YAML.
func buildNotificationPolicyConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      PolicyConfigMapName,
			Namespace: alertingNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "grafana",
				kube.LabelOwner:          kube.OwnerObservability,
			},
		},
		Data: map[string]string{"policies.yaml": NotificationPolicyYAML()},
	}
}
