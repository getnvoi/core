package providers

// notify.go defines the portable types every notify provider returns
// and the per-channel spec they consume. The orchestrator (in
// pkg/internal/observability/grafana) translates Receivers to whichever
// notification system is currently wired (today: Grafana provisioning
// YAML). Notify provider impls have ZERO knowledge of Grafana — they
// know their vendor (Twilio, Postmark, …) and their credentials, and
// they return Receivers.
//
// Mirrors the BucketProvider pattern: the provider abstracts the
// vendor; the consumer wires it to the concrete downstream system.

// Receiver is a portable notification-receiver descriptor. Type names
// the vendor canonically ("twilio", "postmark", "slack"). Settings
// carries non-secret vendor-specific fields. Secrets carries
// references to secret values the orchestrator must materialize in
// the observability namespace before the receiver is usable.
//
// One Receiver per configured channel. The orchestrator collects them
// and renders them into the active notification system's wire format.
type Receiver struct {
	Type     string
	Settings map[string]string
	Secrets  []SecretRef
}

// SecretRef points at a key in a Kubernetes Secret the orchestrator
// must materialize in the observability namespace. Name + Key together
// identify the destination; From is the resolved value (the
// orchestrator stamps the Secret's stringData with this when it
// builds the Secret).
//
// Putting the value on SecretRef (rather than passing separate maps
// around) keeps the Receiver self-contained — one struct fully
// describes what the orchestrator needs to provision this channel.
// Secrets with the same Name are merged on the orchestrator side, so
// a provider that needs two keys in one Secret returns two SecretRefs
// with matching Name + different Key.
type SecretRef struct {
	Name string
	Key  string
	From string
}

// AlertSpec is the resolved per-channel YAML shape every notify
// provider consumes. Provider is the dispatch key (must be a
// registered SMSProvider or EmailProvider). Fields holds the
// vendor-specific rest with $VAR references already resolved at the
// cmd/cli boundary against os.Getenv.
//
// Lives in `providers` (not `config`) because the SHAPE is a provider
// concept — a notification-provider spec — and putting it here lets
// `config` import `providers` (the existing direction) rather than
// the reverse. config.MonitorSpec references *AlertSpec directly.
type AlertSpec struct {
	Provider string                 `yaml:"provider"`
	Fields   map[string]interface{} `yaml:",inline"`
}
