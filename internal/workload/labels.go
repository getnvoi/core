package workload

// namespace is where every nvoi-managed workload lands today. Single
// namespace keeps the substrate simple; per-app namespaces lift in
// when we need isolation.
const namespace = "default"

// Label keys carried by nvoi-managed workloads.
//
// Build* functions stamp kube.LabelOwner explicitly (using
// kube.OwnerServices / kube.OwnerRegistry / kube.OwnerAppSecrets)
// because pod-template labels and Service selectors must include
// the owner to match. kube.ApplyOwned then re-stamps the same value
// at apply time — the redundancy is intentional: Build* outputs are
// self-describing (a manifest is correct without going through
// ApplyOwned to inspect it).
//
// LabelService is the per-service identifier the headless / ClusterIP
// Service uses in its pod selector. Stable across deploys (no
// LabelDeployHash on selectors — selector changes orphan pods).
//
// LabelDeployHash is stamped on workload metadata AND pod-template
// metadata (NEVER on selectors). Reading `kubectl get -L
// nvoi/deploy-hash` answers "which deploy last touched this".
//
// LabelAppName mirrors the upstream nvoi convention
// (`app.kubernetes.io/name`) and is the discriminator the
// topologySpread constraint uses to group a single service's
// replicas. Every pod for service `foo` gets
// `app.kubernetes.io/name: foo`.
//
// LabelNvoiRole is the operator-readable node label applied by
// kube.LabelNode at deploy time, with the YAML server key as its
// value (e.g. "master", "worker-1"). Pods reference it via
// nodeSelector / nodeAffinity for placement.
const (
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
