package cloudflare

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"strconv"
	"text/template"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/internal/compile"
	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/naming"
	"github.com/getnvoi/core/internal/utils"
)

//go:embed templates/tunnel.tf.tmpl
var tunnelTemplateFS embed.FS

var tunnelTpl = template.Must(template.New("tunnel.tf.tmpl").
	Funcs(template.FuncMap{"hcl": strconv.Quote}).
	ParseFS(tunnelTemplateFS, "templates/tunnel.tf.tmpl"))

// TunnelEmitter renders Cloudflare Zero Trust Tunnel resources +
// returns the agent Deployment + Secret the workload phase applies
// post-tf.
//
// HCL contract:
//   - cloudflare_zero_trust_tunnel_cloudflared (the tunnel itself)
//   - cloudflare_zero_trust_tunnel_cloudflared_config (ingress rules
//     from cfg.Domains, with a catch-all 404)
//   - locals.tunnel_cname / locals.tunnel_proxied — DNS emitter reads
//     these to flip records from A → CNAME without coupling
//   - output "tunnel_token" — sensitive; read by the workload phase
type TunnelEmitter struct{}

// Providers declares every terraform provider this tunnel emitter
// relies on:
//
//   - cloudflare (the tunnel + ingress config)
//   - random (the tunnel secret — `cloudflare_zero_trust_tunnel_cloudflared`
//     requires a base64-encoded 32+-byte random secret shared between
//     the agent and the edge). Secret persists in tfstate; that's
//     acceptable for our threat model (state lives in private R2
//     with derived S3 access keys).
//
// compile dedupes by alias when aggregating into backend.tf, so
// declaring cloudflare here alongside the DNS emitter's cloudflare
// declaration produces a single consolidated entry.
func (TunnelEmitter) Providers() []compile.ProviderRequirement {
	return []compile.ProviderRequirement{
		{Alias: "cloudflare", Source: "cloudflare/cloudflare", Version: "~> 4"},
		{Alias: "random", Source: "hashicorp/random", Version: "~> 3"},
	}
}

// TunnelResourceType is the terraform resource type the emitter uses
// for the tunnel object itself. Inspected by the deploy/destroy
// pipeline's drain step before tf-apply: when the saved plan shows
// this resource going to delete (or replace), nvoi pre-emptively
// sweeps the in-cluster cloudflared agent so CF's API doesn't reject
// the tunnel-delete with "active connections" — see
// terraform-provider-cloudflare#5255.
func (TunnelEmitter) TunnelResourceType() string {
	return "cloudflare_zero_trust_tunnel_cloudflared"
}

type tunnelRouteData struct {
	Hostname string
	Service  string // "http://<svc>.<ns>.svc.cluster.local:<port>"
}

type tunnelTemplateData struct {
	AccountID string
	Name      string
	Routes    []tunnelRouteData
}

// EmitTunnel renders cloudflare-tunnel.tf for cfg.Domains.
//
// Reads CF_ACCOUNT_ID env var to scope the tunnel. The CF API token
// (CLOUDFLARE_API_TOKEN, set at the cmd/ boundary) needs
// Account:Cloudflare Tunnel:Edit scope.
func (TunnelEmitter) EmitTunnel(cfg *config.Config) ([]byte, error) {
	accountID := os.Getenv("CF_ACCOUNT_ID")
	if accountID == "" {
		return nil, fmt.Errorf("cloudflare tunnel: CF_ACCOUNT_ID required")
	}

	routes := make([]tunnelRouteData, 0)
	for _, svcName := range utils.SortedKeys(cfg.Domains) {
		svc, ok := cfg.Services[svcName]
		if !ok {
			return nil, fmt.Errorf("cloudflare tunnel: domains references undeclared service %q", svcName)
		}
		for _, host := range cfg.Domains[svcName] {
			routes = append(routes, tunnelRouteData{
				Hostname: host,
				Service:  fmt.Sprintf("http://%s.default.svc.cluster.local:%d", svcName, svc.Port),
			})
		}
	}

	var buf bytes.Buffer
	if err := tunnelTpl.Execute(&buf, tunnelTemplateData{
		AccountID: accountID,
		Name:      naming.Prefix(cfg.App, cfg.Env),
		Routes:    routes,
	}); err != nil {
		return nil, fmt.Errorf("render cloudflare-tunnel.tf: %w", err)
	}
	return buf.Bytes(), nil
}

// AgentWorkloads renders the cloudflared Deployment + Secret. The
// token comes from `terraform output tunnel_token` — read in the
// workload phase, plumbed in here.
//
// Two replicas, sized for a low-traffic public site. Outbound-only:
// no hostPort, no nodeSelector pin (cloudflared dials out, doesn't
// listen). The agent SHOULD work even if master 80/443 is closed.
func (TunnelEmitter) AgentWorkloads(token string) ([]compile.TunnelWorkload, error) {
	if token == "" {
		return nil, fmt.Errorf("cloudflared agent: token required (read from terraform output tunnel_token)")
	}

	const (
		agentName = "cloudflared"
		ns        = "default"
		image     = "cloudflare/cloudflared:2024.8.3"
	)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentName,
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/name": agentName},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"token": []byte(token)},
	}

	two := int32(2)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      agentName,
			Namespace: ns,
			Labels:    map[string]string{"app.kubernetes.io/name": agentName},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &two,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/name": agentName},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app.kubernetes.io/name": agentName},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:    agentName,
						Image:   image,
						Command: []string{"cloudflared"},
						Args: []string{
							"tunnel",
							"--no-autoupdate",
							"--metrics", "0.0.0.0:2000",
							"run",
							"--token", "$(TUNNEL_TOKEN)",
						},
						Env: []corev1.EnvVar{{
							Name: "TUNNEL_TOKEN",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: agentName},
									Key:                  "token",
								},
							},
						}},
						Resources: corev1.ResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("50m"),
								corev1.ResourceMemory: resource.MustParse("64Mi"),
							},
							Limits: corev1.ResourceList{
								corev1.ResourceCPU:    resource.MustParse("200m"),
								corev1.ResourceMemory: resource.MustParse("128Mi"),
							},
						},
					}},
				},
			},
		},
	}

	return []compile.TunnelWorkload{
		{Kind: "Secret", Name: agentName, Obj: secret},
		{Kind: "Deployment", Name: agentName, Obj: dep},
	}, nil
}
