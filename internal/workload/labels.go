package workload

// namespace is where every nvoi-managed workload lands today. Single
// namespace keeps the substrate simple; per-app namespaces lift in
// when we need isolation.
const namespace = "default"

// Label keys carried by every nvoi-managed workload.
//
// LabelOwner / LabelService land on Deployments / StatefulSets / Services /
// Secrets and on every pod-template. LabelOwner=nvoi is the filter
// reconcile-on-removal uses to find what we own.
//
// LabelDeployHash is stamped on workload metadata AND pod-template
// metadata (NEVER on selectors — selector changes orphan pods). Both
// metadata stamps mean `kubectl get -L nvoi/deploy-hash` answers
// "which deploy last touched this".
//
// LabelAppName mirrors the upstream nvoi convention (`app.kubernetes.io/name`)
// and is the discriminator the topologySpread constraint uses to
// group a single service's replicas. Every pod for service `foo`
// gets `app.kubernetes.io/name: foo`.
//
// LabelNvoiRole is the operator-readable node label applied by
// kube.LabelNode at deploy time, with the YAML server key as its
// value (e.g. "master", "worker-1"). Pods reference it via
// nodeSelector / nodeAffinity for placement.
const (
	LabelOwner      = "nvoi/owner"
	LabelService    = "nvoi/service"
	LabelDeployHash = "nvoi/deploy-hash"
	LabelAppName    = "app.kubernetes.io/name"
	LabelNvoiRole   = "nvoi-role"
)

// AppSecretName is the single Opaque Secret in the app namespace that
// holds resolved values for every entry in cfg.Secrets. Per-service
// `secrets:` whitelists drive secretKeyRef-based env injection from
// it; the Secret object itself is created/updated by ApplyAll on
// every deploy.
const AppSecretName = "nvoi-secrets"
