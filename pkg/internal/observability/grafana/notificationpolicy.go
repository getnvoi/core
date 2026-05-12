package grafana

// notificationpolicy.go renders the default Grafana notification
// policy. v1 routing model:
//
//   Every firing alert routes through every configured contact
//   point. No label-based filtering, no per-severity escalation —
//   the simplest possible default. Operators who want sophisticated
//   routing edit via Grafana UI (changes don't persist; reprovisioned
//   on next deploy from this code).
//
// Future: a monitor.routing: YAML block could lift per-severity
// fan-out + on-call rotations into the declarative model. Out of
// scope for v1.

// notificationPolicyYAML is the Grafana provisioning v1 policy.
// receiver: "nvoi-default" is the multi-integration grouper provisioning.go
// builds from every configured contact point.
const notificationPolicyYAML = `apiVersion: 1
policies:
  - orgId: 1
    receiver: nvoi-default
    group_by:
      - grafana_folder
      - alertname
    group_wait: 30s
    group_interval: 5m
    repeat_interval: 4h
`

// NotificationPolicyYAML returns the provisioning YAML. Static for
// v1; exposed as a function so future routing variants (per-severity,
// per-time-of-day) can branch on rt.Monitor without touching call
// sites.
func NotificationPolicyYAML() string {
	return notificationPolicyYAML
}
