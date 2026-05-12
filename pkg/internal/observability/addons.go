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

// MetricsServerURL is the upstream single-file release manifest. The
// master kubectl-applies this URL directly — requires master internet
// (same dependency as the k3s install download and the cert-manager
// manifest).
const MetricsServerURL = "https://github.com/kubernetes-sigs/metrics-server/releases/download/" + MetricsServerVersion + "/components.yaml"

// metricsServerNamespace is where the upstream manifest places the
// Deployment + Service + ServiceAccount. Standard upstream convention.
const metricsServerNamespace = "kube-system"

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

// labelOwned patches `nvoi/owner=<owner>` onto each object in `objs`.
// First failure is treated as fatal IF it's the Deployment (mandatory
// for ListOwned visibility); subsequent failures on auxiliary kinds
// are logged as Warn and the function returns nil.
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
