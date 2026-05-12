package observability

// grafana.go builds Grafana — Deployment (grafana + k8s-sidecar
// containers), ClusterIP Service, admin Secret, optional public
// Ingress + Certificate. The k8s-sidecar container watches the
// nvoi-observability namespace for ConfigMaps with provisioning
// labels (grafana_dashboard=1 / grafana_datasource=1 / grafana_alert=1)
// and drops their content into Grafana's /etc/grafana/provisioning/
// directories. PRs 5+6 populate those ConfigMaps; this file just
// stands up the consumer.
//
// Two access modes:
//   - tunnel-only (default): anonymous viewer auth, reachable only
//     via `nvoi monitor` SSH port-forward.
//   - public: when monitor.domain is set, an Ingress + cert-manager
//     Certificate land alongside, with real admin auth.

import (
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/runtime"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	grafanaComponent       = "grafana"
	grafanaAdminSecretName = "grafana-admin"
	grafanaConfigName      = "grafana-config"
	grafanaIngressClass    = "traefik" // matches workload.ingressClassName
)

// tunnelAdminPassword is the throwaway admin password used in
// tunnel-only mode (when no operator-supplied admin_password). The
// SSH tunnel itself is the access control; the password is shown to
// the operator on `nvoi monitor` start so they can sign in for
// personal features (favorites, stars). Not a real secret —
// reachable only via SSH-tunneled localhost.
const tunnelAdminPassword = "nvoi-tunnel-only" //nolint:gosec // not a real secret in this mode

// AdminPassword returns the Grafana admin password for this runtime.
// Operator-supplied (monitor.admin_password) wins; tunnel-mode
// falls back to the sentinel.
//
// Single source of truth: buildGrafanaAdminSecret and
// pkg/deploy.Monitor both call this. Without it the literal
// "nvoi-tunnel-only" was duplicated across packages — change the
// sentinel, change two call sites silently. Now: one.
func AdminPassword(rt *runtime.Runtime) string {
	if rt != nil && rt.Monitor != nil && rt.Monitor.AdminPassword != "" {
		return rt.Monitor.AdminPassword
	}
	return tunnelAdminPassword
}

// buildGrafanaAdminSecret renders the admin-password Secret. In
// tunnel-only mode (rt.Monitor.AdminPassword == "") we still emit
// the Secret with the tunnel-only sentinel so Grafana's env-from-Secret
// reference always resolves — no conditional env wiring on the
// Deployment side.
func buildGrafanaAdminSecret(rt *runtime.Runtime) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaAdminSecretName,
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"admin-password": AdminPassword(rt)},
	}
}

// buildGrafanaConfigMap renders grafana.ini. Anonymous is ALWAYS
// disabled — Grafana's anonymous mode is read-only at the user
// level (favorites / stars / preferences are user-bound and refuse
// to write with no user identity, surfacing as "Unauthorized" the
// instant the operator tries to star anything). Better UX: force
// admin sign-in with the per-deploy password, log it on
// `nvoi monitor` start.
//
// Two access modes:
//   - tunnel-only (domain == ""): root_url=localhost; reached via
//     `nvoi monitor`. SSH tunnel is the access control.
//   - public (domain set):       root_url=https://<domain>/; reached
//     directly via Ingress + cert-manager TLS.
//
// Admin password comes from the Grafana admin Secret regardless of
// mode (sentinel in tunnel-only; operator-supplied via $VAR when
// domain is set).
func buildGrafanaConfigMap(rt *runtime.Runtime) *corev1.ConfigMap {
	domain := ""
	if rt.Monitor != nil {
		domain = rt.Monitor.Domain
	}

	rootURL := "http://localhost:3000/"
	if domain != "" {
		rootURL = "https://" + domain + "/"
	}

	ini := `[server]
root_url = ` + rootURL + `
serve_from_sub_path = false

[auth.anonymous]
enabled = false

[security]
allow_embedding = false
`
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaConfigName,
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
		Data: map[string]string{"grafana.ini": ini},
	}
}

// buildGrafanaDeployment renders the grafana + k8s-sidecar pod.
//
// Provisioning layout (paths under /etc/grafana/provisioning/):
//
//   - datasources/   — direct ConfigMap mount (grafana-datasources).
//     Static; Grafana loads at boot.
//   - alerting/      — projected volume combining grafana-contact-points
//                      + grafana-notification-policy + alert-rules
//                      ConfigMaps, each `optional: true`. Static;
//                      Grafana loads at boot.
//   - dashboards/    — emptyDir written by the k8s-sidecar that
//                      watches grafana_dashboard=1 ConfigMaps in the
//                      namespace. Hot-reloads as dashboards land.
//                      A static dashboards-provider ConfigMap mounts
//                      one file (provider.yaml) into the same dir so
//                      Grafana knows to scan the folder.
//
// optional: true on every projected source lets the Deployment stay
// static — Grafana boots fine when alerts ConfigMaps don't exist (no
// monitor.alerts configured).
//
// SMTP env wiring: when rt.Monitor.Alerts.Email is configured, the
// container gets GF_SMTP_* env vars sourced from the per-provider
// creds Secret. Grafana's "email" contact-point handler requires
// SMTP configured at the server level — without these env vars, the
// contact-point exists but no mail ever sends.
func buildGrafanaDeployment(rt *runtime.Runtime) *appsv1.Deployment {
	replicas := int32(1)
	optional := true
	env := buildGrafanaEnv(rt)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaComponent,
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: selectorFor(grafanaComponent)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels(grafanaComponent)},
				Spec: corev1.PodSpec{
					// Grafana's sidecar watches ConfigMaps in the
					// namespace; default SA can't, so we bind a
					// minimal Role to a dedicated SA. Without this the
					// dashboards never load (403 on configmap watch).
					ServiceAccountName: grafanaComponent,
					// initContainer copies the dashboards provider config
					// into the dashboards emptyDir so Grafana knows to
					// scan the path for JSON files. The sidecar writes
					// dashboards into the same dir at runtime.
					InitContainers: []corev1.Container{{
						Name:    "dashboards-provider-init",
						Image:   "busybox:1.36",
						Command: []string{"sh", "-c", "cp /provider/dashboards.yaml /dashboards/dashboards.yaml"},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "dashboards-provider", MountPath: "/provider"},
							{Name: "dashboards", MountPath: "/dashboards"},
						},
						Resources: stdRequests("10m", "16Mi"),
					}},
					Containers: []corev1.Container{
						{
							Name:  "grafana",
							Image: GrafanaImage,
							Env:   env,
							Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 3000}},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config", MountPath: "/etc/grafana/grafana.ini", SubPath: "grafana.ini"},
								{Name: "datasources", MountPath: "/etc/grafana/provisioning/datasources"},
								{Name: "alerting", MountPath: "/etc/grafana/provisioning/alerting"},
								{Name: "dashboards", MountPath: "/etc/grafana/provisioning/dashboards"},
								{Name: "data", MountPath: "/var/lib/grafana"},
							},
							Resources: stdRequests("100m", "128Mi"),
						},
						{
							Name:  "sidecar",
							Image: GrafanaSidecar,
							Env: []corev1.EnvVar{
								{Name: "LABEL", Value: "grafana_dashboard"},
								{Name: "LABEL_VALUE", Value: "1"},
								{Name: "FOLDER", Value: "/etc/grafana/provisioning/dashboards"},
								{Name: "RESOURCE", Value: "configmap"},
								{Name: "NAMESPACE", Value: Namespace},
								{Name: "WATCH_METHOD", Value: "WATCH"},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "dashboards", MountPath: "/etc/grafana/provisioning/dashboards"},
							},
							Resources: stdRequests("50m", "64Mi"),
						},
					},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: grafanaConfigName}},
						}},
						{Name: "datasources", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: grafanaDatasourcesName}},
						}},
						// alerting is a projected volume combining the three
						// optional alert-provisioning ConfigMaps. Each
						// `optional: true` so Grafana boots cleanly when
						// monitor.alerts is unset.
						{Name: "alerting", VolumeSource: corev1.VolumeSource{
							Projected: &corev1.ProjectedVolumeSource{
								Sources: []corev1.VolumeProjection{
									{ConfigMap: &corev1.ConfigMapProjection{
										LocalObjectReference: corev1.LocalObjectReference{Name: grafanaContactPointsName},
										Optional:             &optional,
									}},
									{ConfigMap: &corev1.ConfigMapProjection{
										LocalObjectReference: corev1.LocalObjectReference{Name: grafanaPolicyName},
										Optional:             &optional,
									}},
									{ConfigMap: &corev1.ConfigMapProjection{
										LocalObjectReference: corev1.LocalObjectReference{Name: alertRulesConfigMapName},
										Optional:             &optional,
									}},
									// Dashboards provider config — points Grafana
									// at /etc/grafana/provisioning/dashboards.
									// NOTE: belongs in /dashboards/, not /alerting/.
									// Moved out below; included in the dashboards
									// projected volume instead.
								},
							},
						}},
						// Dashboards: emptyDir the sidecar writes dashboard
						// JSON files into. The initContainer pre-populates
						// dashboards.yaml (provider config) into the same
						// dir so Grafana auto-loads the JSON files.
						{Name: "dashboards", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "dashboards-provider", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{
								LocalObjectReference: corev1.LocalObjectReference{Name: grafanaDashboardsProviderName},
							},
						}},
						{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
}

// Provisioning ConfigMap names referenced by buildGrafanaDeployment's
// projected volume. Duplicated here (alongside grafana.subpackage's
// constants) so this file stays self-contained and pkg/internal/
// observability doesn't need to import its own subpackage to compose.
const (
	grafanaDatasourcesName        = "grafana-datasources"
	grafanaContactPointsName      = "grafana-contact-points"
	grafanaPolicyName             = "grafana-notification-policy"
	grafanaDashboardsProviderName = "grafana-dashboards-provider"
)

// buildDashboardsProviderConfigMap renders the dashboards provisioning
// provider config Grafana needs at /etc/grafana/provisioning/dashboards/
// to know it should scan that path for JSON files. Without this,
// dashboard ConfigMaps land in the dir (via the kiwigrid sidecar) but
// Grafana never loads them — Grafana only loads dashboards when a
// provider config tells it where to look.
//
// Static content; one ConfigMap, one key (dashboards.yaml). Copied
// into the dashboards emptyDir at pod start by an initContainer
// (see buildGrafanaDeployment). Can't mount via subPath alongside
// the emptyDir — k8s doesn't allow it.
func buildDashboardsProviderConfigMap() *corev1.ConfigMap {
	const cfg = `apiVersion: 1
providers:
  - name: nvoi
    orgId: 1
    folder: nvoi
    type: file
    disableDeletion: false
    updateIntervalSeconds: 30
    allowUiUpdates: false
    options:
      path: /etc/grafana/provisioning/dashboards
      foldersFromFilesStructure: false
`
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaDashboardsProviderName,
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
		Data: map[string]string{"dashboards.yaml": cfg},
	}
}

// buildGrafanaRBAC returns the ServiceAccount + Role + RoleBinding
// the Grafana sidecar needs to watch ConfigMaps in the observability
// namespace. Namespace-scoped (not cluster-scoped): the sidecar only
// reads ConfigMaps in its own namespace, so minimal-privilege RBAC.
//
// Returns three objects in apply order: SA, Role, RoleBinding.
func buildGrafanaRBAC() (*corev1.ServiceAccount, *rbacv1.Role, *rbacv1.RoleBinding) {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaComponent,
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
	}
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaComponent + "-sidecar",
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"configmaps", "secrets"},
				Verbs:     []string{"get", "list", "watch"},
			},
		},
	}
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaComponent + "-sidecar",
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     grafanaComponent + "-sidecar",
		},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      grafanaComponent,
			Namespace: Namespace,
		}},
	}
	return sa, role, rb
}

// buildGrafanaEnv composes the Grafana container's env-var list.
// Admin password is always present (sourced from grafana-admin Secret).
// SMTP env vars conditional on monitor.alerts.email being a Postmark
// receiver — they wire Grafana's [smtp] section to Postmark's SMTP
// endpoint using the per-provider Secret materialized in 6a.
//
// Future email providers (sendgrid, ses, generic SMTP) extend the
// switch here. Hardcoded vendor mapping is fine for v1 — when the
// second provider lands, lift this into a per-EmailProvider method
// returning []corev1.EnvVar.
func buildGrafanaEnv(rt *runtime.Runtime) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{
			Name: "GF_SECURITY_ADMIN_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: grafanaAdminSecretName},
					Key:                  "admin-password",
				},
			},
		},
		{Name: "GF_PATHS_PROVISIONING", Value: "/etc/grafana/provisioning"},
	}

	if rt == nil || rt.Monitor == nil || rt.Monitor.Alerts == nil || rt.Monitor.Alerts.Email == nil {
		return env
	}
	email := rt.Monitor.Alerts.Email
	switch email.Provider {
	case "postmark":
		from, _ := email.Fields["from"].(string)
		env = append(env,
			corev1.EnvVar{Name: "GF_SMTP_ENABLED", Value: "true"},
			corev1.EnvVar{Name: "GF_SMTP_HOST", Value: "smtp.postmarkapp.com:587"},
			corev1.EnvVar{
				Name: "GF_SMTP_USER",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "postmark-creds"},
						Key:                  "token",
					},
				},
			},
			corev1.EnvVar{
				Name: "GF_SMTP_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: "postmark-creds"},
						Key:                  "token",
					},
				},
			},
			corev1.EnvVar{Name: "GF_SMTP_FROM_ADDRESS", Value: from},
			corev1.EnvVar{Name: "GF_SMTP_FROM_NAME", Value: "nvoi"},
			corev1.EnvVar{Name: "GF_SMTP_SKIP_VERIFY", Value: "false"},
		)
	}
	return env
}

// buildGrafanaService returns the ClusterIP Service exposing :3000
// (Grafana's HTTP port). `nvoi monitor` port-forwards through this
// Service; the public Ingress (when active) routes through it too.
func buildGrafanaService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaComponent,
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selectorFor(grafanaComponent),
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 3000, TargetPort: intstr.FromString("http")},
			},
		},
	}
}

// buildGrafanaIngress renders the public Ingress targeting Grafana
// when monitor.domain is set. Returns nil when domain is empty
// (tunnel-only mode → no public exposure).
//
// TLS Secret name is derived the same way pkg/workload/ingress.go
// derives app-workload TLS — kube.SanitizeDNS1123(host)+"-tls" —
// so a per-domain Certificate resource (PR 6) lands the Secret in
// nvoi-observability where this Ingress references it.
func buildGrafanaIngress(rt *runtime.Runtime) *networkingv1.Ingress {
	if rt.Monitor == nil || rt.Monitor.Domain == "" {
		return nil
	}
	host := rt.Monitor.Domain
	secretName := kube.SanitizeDNS1123(host) + "-tls"

	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaComponent,
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(grafanaIngressClass),
			TLS: []networkingv1.IngressTLS{{
				Hosts:      []string{host},
				SecretName: secretName,
			}},
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: ptr.To(networkingv1.PathTypePrefix),
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: grafanaComponent,
									Port: networkingv1.ServiceBackendPort{Number: 3000},
								},
							},
						}},
					},
				},
			}},
		},
	}
}
