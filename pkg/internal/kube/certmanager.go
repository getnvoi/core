// Package kube — certmanager.go installs cert-manager onto the cluster
// and applies the ClusterIssuer + per-domain Certificate resources that
// drive Let's Encrypt issuance via the operator's chosen DNS provider.
//
// We don't talk to cert-manager via the typed client — its CRDs aren't
// in our scheme, and adding generic dynamic-client support is a real
// expansion. Instead the applier shells out to `sudo k3s kubectl
// apply -f -` over the master's SSH session: simple, no extra deps,
// matches how operators would apply third-party manifests by hand.
package kube

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/internal/compile"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/ssh"
)

// CertManagerVersion pins the cert-manager release we apply. Bumping
// this is a one-line change; review release notes for breaking CRD
// changes before doing so.
const CertManagerVersion = "v1.16.2"

// CertManagerURL is the upstream single-file manifest GitHub release
// URL. The master runs `kubectl apply -f` against this URL directly —
// requires master internet (same dependency as the k3s install
// download).
const CertManagerURL = "https://github.com/cert-manager/cert-manager/releases/download/" + CertManagerVersion + "/cert-manager.yaml"

// CertManagerNamespace is where cert-manager pods + provider Secrets
// (e.g. cloudflare-api-token) live. Standard upstream convention.
const CertManagerNamespace = "cert-manager"

// ClusterIssuerName is the singleton ClusterIssuer name nvoi creates.
// Per-domain Certificate resources reference it via issuerRef.
const ClusterIssuerName = "letsencrypt"

// ApplyCertManager installs cert-manager onto the cluster and waits
// for its three Deployments (controller, cainjector, webhook) to be
// Available. Idempotent — re-running is a no-op kubectl apply.
//
// Master is the SSH session into the primary master; cert-manager runs
// as a Deployment so it'll schedule wherever k8s puts it (any node).
func ApplyCertManager(ctx context.Context, sh ssh.Shell, lg log.Log) error {
	lg.Step("cert-manager-install")
	out, err := install.Kubectl(ctx, sh, "apply", "-f", CertManagerURL)
	if err != nil {
		return fmt.Errorf("apply cert-manager %s: %w (out: %s)", CertManagerVersion, err, out)
	}
	lg.Info(fmt.Sprintf("cert-manager %s applied", CertManagerVersion))

	lg.Step("cert-manager-ready")
	for _, dep := range []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"} {
		out, err := install.Kubectl(ctx, sh,
			"-n", CertManagerNamespace,
			"wait", "--for=condition=Available",
			"--timeout=180s",
			"deployment/"+dep,
		)
		if err != nil {
			return fmt.Errorf("wait %s: %w (out: %s)", dep, err, out)
		}
	}
	lg.Info("cert-manager ready")
	return nil
}

// ApplyYAML pipes raw YAML to `kubectl apply -f -` on the master via
// base64 indirection (avoids shell-quoting hell for arbitrary YAML).
// Used for ClusterIssuer + Certificate + provider-secret manifests
// that nvoi generates.
func ApplyYAML(ctx context.Context, sh ssh.Shell, yaml []byte) error {
	if len(yaml) == 0 {
		return nil
	}
	enc := base64.StdEncoding.EncodeToString(yaml)
	cmd := fmt.Sprintf("echo %s | base64 -d | sudo k3s kubectl apply -f -", enc)
	out, err := sh.Run(ctx, cmd)
	if err != nil {
		return fmt.Errorf("kubectl apply: %w (out: %s)", err, out)
	}
	return nil
}

// BuildSolverSecretsYAML renders one Secret manifest per
// compile.SolverSecret cert-manager needs (DNS provider's credential),
// all in CertManagerNamespace. Empty input returns nil.
func BuildSolverSecretsYAML(secrets []compile.SolverSecret) []byte {
	if len(secrets) == 0 {
		return nil
	}
	var b strings.Builder
	for i, s := range secrets {
		if i > 0 {
			b.WriteString("---\n")
		}
		fmt.Fprintf(&b,
			"apiVersion: v1\nkind: Secret\nmetadata:\n  name: %s\n  namespace: %s\ntype: Opaque\nstringData:\n  %s: %q\n",
			s.Name, CertManagerNamespace, s.Key, s.Value,
		)
	}
	return []byte(b.String())
}

// BuildClusterIssuerYAML renders the singleton ClusterIssuer that all
// per-domain Certificate resources reference. The solver YAML is the
// inner element of `spec.acme.solvers` (the DNS provider supplies it
// via DNSEmitter.CertManagerSolver).
//
// email is the ACME contact (rate-limit + expiry warnings land there);
// when empty, falls back to acme@<first-domain> to match Caddy's prior
// behavior in nvoi.
func BuildClusterIssuerYAML(email, solverYAML string) []byte {
	if email == "" {
		email = "acme@nvoi.local"
	}
	return []byte(fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: %s
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: %s
    privateKeySecretRef:
      name: %s-account
    solvers:
%s
`, ClusterIssuerName, email, ClusterIssuerName, solverYAML))
}

// BuildCertificateYAML renders one Certificate per domain. cert-manager
// will issue + auto-renew. Output Secret name (DNS-1123-shaped) is
// returned so the Ingress-emitter can reference it from its TLS block.
func BuildCertificateYAML(namespace, domain string) (yaml []byte, secretName string) {
	name := SanitizeDNS1123(domain)
	secretName = name + "-tls"
	yaml = []byte(fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: %s
  namespace: %s
spec:
  secretName: %s
  issuerRef:
    name: %s
    kind: ClusterIssuer
  dnsNames:
  - %s
`, name, namespace, secretName, ClusterIssuerName, domain))
	return yaml, secretName
}

// SanitizeDNS1123 rewrites a domain to a valid k8s resource name —
// lowercase, digits + dashes, no dots. Used as the per-domain key for
// Certificate + Secret names.
func SanitizeDNS1123(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "x"
	}
	return out
}
