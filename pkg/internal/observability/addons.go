// Package observability owns the cluster-side monitoring + addons
// pipeline. addons.go installs cluster-level prerequisites that
// every deploy benefits from regardless of whether the operator
// activates the full observability stack.
//
// metrics-server today; future cluster-wide addons (cluster-autoscaler,
// node-problem-detector, …) belong here too. All addons land under
// `nvoi/owner=addons` so SweepOwned manages their lifecycle as a
// single closed scope.
//
// Same shell-out pattern as pkg/internal/kube/certmanager.go: a
// single-file upstream manifest gets `kubectl apply -f <URL>`'d over
// the primary master's SSH session, then we wait for the expected
// Deployment(s) to be Available, then patch the owner label onto each
// object the manifest creates. No typed client involvement — these
// addons ship their own CRDs (metrics-server has APIService) and
// pulling them into our scheme is overkill for a label-patch.
package observability

import (
	"context"
	"fmt"
	"strings"

	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/ssh"
)

// MetricsServerVersion pins the metrics-server release. Bump as a
// one-line change; review the release's CRD / APIService changes
// before doing so.
const MetricsServerVersion = "v0.7.2"

// KubeStateMetricsVersion pins the kube-state-metrics release.
// kube-state-metrics exposes Kubernetes object state as Prometheus
// metrics (kube_deployment_status_replicas_ready, kube_pod_...,
// etc.) — required by the dashboards + alert rules nvoi generates
// from cfg.Services. Without it, panels render empty and alerts
// silently never fire.
const KubeStateMetricsVersion = "v2.13.0"

// MetricsServerURL is the upstream single-file release manifest. The
// master kubectl-applies this URL directly — requires master internet
// (same dependency as the k3s install download and the cert-manager
// manifest).
const MetricsServerURL = "https://github.com/kubernetes-sigs/metrics-server/releases/download/" + MetricsServerVersion + "/components.yaml"

// KubeStateMetricsURL is the upstream kustomize-rendered single-file
// manifest for the "standard" install (kube-state-metrics 2.x ships
// pre-rendered manifests in the release tarball; we pull the GitHub
// raw blob that's stable across point releases).
const KubeStateMetricsURL = "https://raw.githubusercontent.com/kubernetes/kube-state-metrics/" + KubeStateMetricsVersion + "/examples/standard/cluster-role-binding.yaml"

// Additional kube-state-metrics manifests applied in order. The
// upstream "standard" install is split across multiple YAML files
// (cluster-role, cluster-role-binding, deployment, service-account,
// service). We apply each; kubectl handles them as a single
// reconciliation unit.
var kubeStateMetricsURLs = []string{
	"https://raw.githubusercontent.com/kubernetes/kube-state-metrics/" + KubeStateMetricsVersion + "/examples/standard/cluster-role.yaml",
	"https://raw.githubusercontent.com/kubernetes/kube-state-metrics/" + KubeStateMetricsVersion + "/examples/standard/cluster-role-binding.yaml",
	"https://raw.githubusercontent.com/kubernetes/kube-state-metrics/" + KubeStateMetricsVersion + "/examples/standard/service-account.yaml",
	"https://raw.githubusercontent.com/kubernetes/kube-state-metrics/" + KubeStateMetricsVersion + "/examples/standard/deployment.yaml",
	"https://raw.githubusercontent.com/kubernetes/kube-state-metrics/" + KubeStateMetricsVersion + "/examples/standard/service.yaml",
}

// metricsServerNamespace is where the upstream manifest places the
// Deployment + Service + ServiceAccount. Standard upstream convention.
const metricsServerNamespace = "kube-system"

// kubeStateMetricsNamespace matches the upstream manifest default.
const kubeStateMetricsNamespace = "kube-system"

// ownedObjects is the closed list of objects in the upstream manifest
// we want SweepOwned to be able to manage. metrics-server installs
// additional resources (RoleBinding to extension-apiserver-authentication,
// APIService, etc.) — those stay un-labeled because they're plumbing
// the cluster needs regardless of who installed metrics-server. We
// only stamp the operator-visible ones.
type ownedObject struct {
	resource string // kubectl resource type (e.g. "deployment", "service")
	scope    string // "-n kube-system" for namespaced, "" for cluster-scoped
	name     string
}

var metricsServerOwned = []ownedObject{
	{resource: "deployment", scope: "-n " + metricsServerNamespace, name: "metrics-server"},
	{resource: "service", scope: "-n " + metricsServerNamespace, name: "metrics-server"},
	{resource: "serviceaccount", scope: "-n " + metricsServerNamespace, name: "metrics-server"},
	{resource: "apiservice", scope: "", name: "v1beta1.metrics.k8s.io"},
}

// kubeStateMetricsOwned lists the operator-visible objects the
// upstream manifest creates. Deployment label is mandatory (ListOwned
// filters by it); the rest are best-effort.
var kubeStateMetricsOwned = []ownedObject{
	{resource: "deployment", scope: "-n " + kubeStateMetricsNamespace, name: "kube-state-metrics"},
	{resource: "service", scope: "-n " + kubeStateMetricsNamespace, name: "kube-state-metrics"},
	{resource: "serviceaccount", scope: "-n " + kubeStateMetricsNamespace, name: "kube-state-metrics"},
	{resource: "clusterrole", scope: "", name: "kube-state-metrics"},
	{resource: "clusterrolebinding", scope: "", name: "kube-state-metrics"},
}

// ApplyMetricsServer installs metrics-server onto the cluster, waits
// for its Deployment to be Available, and stamps `nvoi/owner=addons`
// on the operator-visible resources. Idempotent — re-running is a
// no-op kubectl apply + a no-op label patch.
//
// sh is the primary master's SSH session (the same one the rest of
// the deploy lifecycle uses for kubectl). lg is expected to already
// be scoped to KindCluster — the caller in pkg/deploy uses s.Lg.
//
// Failure to label one of the auxiliary objects (service / SA /
// apiservice) is a Warn, not an error: the Deployment label is
// what ListOwned would query against, and a metrics-server install
// without a service-account label still works correctly. The
// Deployment label is mandatory.
func ApplyMetricsServer(ctx context.Context, sh ssh.Shell, lg log.Log) error {
	lg.Step("metrics-server-install")
	out, err := install.Kubectl(ctx, sh, "apply", "-f", MetricsServerURL)
	if err != nil {
		return fmt.Errorf("apply metrics-server %s: %w (out: %s)", MetricsServerVersion, err, out)
	}
	lg.Info(fmt.Sprintf("metrics-server %s applied", MetricsServerVersion))

	lg.Step("metrics-server-ready")
	out, err = install.Kubectl(ctx, sh,
		"-n", metricsServerNamespace,
		"wait", "--for=condition=Available",
		"--timeout=180s",
		"deployment/metrics-server",
	)
	if err != nil {
		return fmt.Errorf("wait metrics-server: %w (out: %s)", err, out)
	}

	lg.Step("metrics-server-label")
	if err := labelOwned(ctx, sh, lg, metricsServerOwned, kube.OwnerAddons); err != nil {
		return err
	}
	lg.Info("metrics-server ready")
	return nil
}

// ApplyKubeStateMetrics installs kube-state-metrics onto the cluster,
// waits for its Deployment to be Available, and stamps
// `nvoi/owner=addons` on the operator-visible objects. Idempotent.
//
// Required by the monitor: stack — dashboards and alerts reference
// kube_* metrics this exporter produces. Installed unconditionally
// (cheap; ~30Mi resident) so single-deploy flips into `monitor:` work
// without an additional manual install step.
//
// Same shell-out pattern as ApplyCertManager / ApplyMetricsServer —
// apply each upstream manifest in order, wait Available, label.
func ApplyKubeStateMetrics(ctx context.Context, sh ssh.Shell, lg log.Log) error {
	lg.Step("kube-state-metrics-install")
	for _, url := range kubeStateMetricsURLs {
		out, err := install.Kubectl(ctx, sh, "apply", "-f", url)
		if err != nil {
			return fmt.Errorf("apply kube-state-metrics %s: %w (out: %s)", url, err, out)
		}
	}
	lg.Info(fmt.Sprintf("kube-state-metrics %s applied", KubeStateMetricsVersion))

	lg.Step("kube-state-metrics-ready")
	out, err := install.Kubectl(ctx, sh,
		"-n", kubeStateMetricsNamespace,
		"wait", "--for=condition=Available",
		"--timeout=180s",
		"deployment/kube-state-metrics",
	)
	if err != nil {
		return fmt.Errorf("wait kube-state-metrics: %w (out: %s)", err, out)
	}

	lg.Step("kube-state-metrics-label")
	if err := labelOwned(ctx, sh, lg, kubeStateMetricsOwned, kube.OwnerAddons); err != nil {
		return err
	}
	lg.Info("kube-state-metrics ready")
	return nil
}

// labelOwned patches `nvoi/owner=<owner>` onto each object in `objs`.
// Deployment failures are fatal (ListOwned filters by them);
// auxiliary kinds (service / SA / clusterrole / apiservice) are
// best-effort and warn on failure.
//
// Closed-list iteration: the caller picks WHICH addon objects to
// stamp; this function just runs the patches. Easy to extend for
// future addons by adding more ownedObject entries.
func labelOwned(ctx context.Context, sh ssh.Shell, lg log.Log, objs []ownedObject, owner string) error {
	for _, o := range objs {
		args := []string{}
		if o.scope != "" {
			args = append(args, strings.Fields(o.scope)...)
		}
		args = append(args, "label", o.resource, o.name,
			kube.LabelOwner+"="+owner, "--overwrite")
		out, err := install.Kubectl(ctx, sh, args...)
		if err != nil {
			// Deployment is mandatory — ListOwned filters by it.
			// Anything else is best-effort.
			if o.resource == "deployment" {
				return fmt.Errorf("label %s/%s: %w (out: %s)", o.resource, o.name, err, out)
			}
			lg.Warn(fmt.Sprintf("label %s/%s: %v (out: %s)", o.resource, o.name, err, out))
		}
	}
	return nil
}
