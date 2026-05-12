// Package kube — tunnel.go installs Cloudflare's cloudflared as a
// system workload routing the cluster's per-domain ingress via an
// outbound tunnel to CF's edge. Owner-labeled under OwnerTunnel for
// SweepOwned lifecycle management.
//
// The companion ingress configuration (which hostnames map to which
// in-cluster Services) is owned by tofu — the
// `cloudflare_zero_trust_tunnel_cloudflared_config` resource the
// cloudflare DNS emitter renders in tunnel mode. cloudflared with
// `--token` consumes remote-managed config; nvoi never writes a
// local config.yaml. Single source of truth for tunnel routes is
// the tofu state.
package kube

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/getnvoi/core/pkg/log"
)

// CloudflaredVersion pins the cloudflared image. Bumping this is a
// one-line change; review release notes for tunnel protocol changes.
// cloudflare/cloudflared publishes multi-arch images (amd64 + arm64)
// so it schedules cleanly on Hetzner cax* and cpx* alike.
const CloudflaredVersion = "2025.4.0"

// CloudflaredImage is the upstream image reference.
const CloudflaredImage = "cloudflare/cloudflared:" + CloudflaredVersion

// TunnelNamespace isolates cloudflared from application workloads in
// `default`. Cross-namespace Service DNS resolves through CoreDNS
// natively — cloudflared upstreams `<svc>.default.svc.cluster.local`
// from this namespace without any extra wiring.
const TunnelNamespace = "nvoi-tunnel"

// TunnelDeploymentName / TunnelSecretName are stable singletons inside
// TunnelNamespace. SweepOwned uses owner=tunnel for lifecycle, but
// fixed names make `kubectl get -n nvoi-tunnel deploy cloudflared`
// debugging frictionless.
const (
	TunnelDeploymentName = "cloudflared"
	TunnelSecretName     = "cloudflared-token"
)

// TunnelSpec is the narrow input ApplyTunnel needs. Bundled so the
// method signature stays (ctx, lg, spec) — the >4-args rule.
type TunnelSpec struct {
	// Token is the base64-encoded JSON blob cloudflared consumes via
	// `--token`. Produced by the tofu `tunnel` output's `.token`
	// field; nvoi reads it post-apply and threads it here.
	Token string

	// Replicas is the cloudflared Deployment size. ≥2 gives CF edge
	// connection redundancy. Caller defaults to 2 in PR 5 wiring.
	Replicas int32
}

// ApplyTunnel ensures TunnelNamespace exists, applies the token Secret
// + cloudflared Deployment, and stamps owner=tunnel on both via
// ApplyOwned. Idempotent — re-running on an unchanged cluster is a
// read-then-no-op via apply.go's Get-then-Update path.
func (c *Client) ApplyTunnel(ctx context.Context, lg log.Log, spec TunnelSpec) error {
	if spec.Token == "" {
		return fmt.Errorf("ApplyTunnel: empty token (tofu tunnel output not populated)")
	}
	if spec.Replicas == 0 {
		spec.Replicas = 2
	}

	lg.Step("tunnel-namespace")
	if err := c.ensureNamespace(ctx, TunnelNamespace); err != nil {
		return fmt.Errorf("ensure namespace %s: %w", TunnelNamespace, err)
	}

	scope := Scope{Namespace: TunnelNamespace, Owner: OwnerTunnel}

	lg.Step("tunnel-secret")
	if err := c.ApplyOwned(ctx, scope, buildTunnelSecret(spec.Token)); err != nil {
		return fmt.Errorf("apply tunnel secret: %w", err)
	}

	lg.Step("tunnel-deployment")
	if err := c.ApplyOwned(ctx, scope, buildTunnelDeployment(spec.Replicas)); err != nil {
		return fmt.Errorf("apply tunnel deployment: %w", err)
	}

	lg.Info(fmt.Sprintf("cloudflared %s applied (replicas=%d)", CloudflaredVersion, spec.Replicas))
	return nil
}

// SweepTunnel removes the cloudflared Deployment + token Secret under
// owner=tunnel. Called when ingress mode flips from cloudflare to
// traefik (PR 5 wiring) — no-op when nothing matches.
//
// The namespace itself is left in place. Empty namespaces are cheap
// and leave debugging breadcrumbs; a future flip back to tunnel mode
// reuses the same ns.
func (c *Client) SweepTunnel(ctx context.Context, lg log.Log) error {
	scope := Scope{Namespace: TunnelNamespace, Owner: OwnerTunnel}
	if err := c.SweepOwned(ctx, scope, KindDeployment, nil); err != nil {
		return fmt.Errorf("sweep tunnel deployments: %w", err)
	}
	if err := c.SweepOwned(ctx, scope, KindSecret, nil); err != nil {
		return fmt.Errorf("sweep tunnel secrets: %w", err)
	}
	lg.Info("tunnel workload swept")
	return nil
}

// buildTunnelSecret holds the base64 token cloudflared consumes via
// `--token`. Token is sensitive — only present in cluster state,
// never logged or surfaced in non-secret YAML.
func buildTunnelSecret(token string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TunnelSecretName,
			Namespace: TunnelNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"token": token,
		},
	}
}

// buildTunnelDeployment renders the cloudflared Deployment. Multi-
// replica registers each replica to the same tunnel UUID — CF edge
// load-balances inbound traffic across the replica set. cloudflared
// has no inbound listener (egress-only outbound to CF edge), so no
// Service is required.
//
// Resource requests reflect cloudflared's footprint: ~30Mi resident
// per replica in steady state. Tolerations: none — schedules on any
// node. Spread soft: ScheduleAnyway preserves Ready on single-node
// clusters where DoNotSchedule would leave a replica Pending.
func buildTunnelDeployment(replicas int32) *appsv1.Deployment {
	labels := map[string]string{
		"app.kubernetes.io/name":      "cloudflared",
		"app.kubernetes.io/component": "ingress-tunnel",
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      TunnelDeploymentName,
			Namespace: TunnelNamespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To(replicas),
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "cloudflared",
						Image: CloudflaredImage,
						Args: []string{
							"tunnel",
							"--no-autoupdate",
							// --metrics binds /ready + /metrics on the
							// pod IP so the readinessProbe can reach
							// them without --network=host.
							"--metrics", "0.0.0.0:2000",
							"run",
							"--token", "$(TUNNEL_TOKEN)",
						},
						Env: []corev1.EnvVar{{
							Name: "TUNNEL_TOKEN",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: TunnelSecretName,
									},
									Key: "token",
								},
							},
						}},
						Ports: []corev1.ContainerPort{{
							Name:          "metrics",
							ContainerPort: 2000,
							Protocol:      corev1.ProtocolTCP,
						}},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								HTTPGet: &corev1.HTTPGetAction{
									Path: "/ready",
									Port: intstr.FromInt32(2000),
								},
							},
							PeriodSeconds:    10,
							FailureThreshold: 3,
						},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("100m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceMemory: resource.MustParse("256Mi"),
							},
						},
					}},
					TopologySpreadConstraints: []corev1.TopologySpreadConstraint{{
						MaxSkew:           1,
						TopologyKey:       "kubernetes.io/hostname",
						WhenUnsatisfiable: corev1.ScheduleAnyway,
						LabelSelector:     &metav1.LabelSelector{MatchLabels: labels},
					}},
				},
			},
		},
	}
}
