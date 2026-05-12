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

// grafanaAnonPassword is the throwaway admin password used in
// tunnel-only mode (when no operator-supplied admin_password). The
// SSH tunnel itself is the access control; admin login is disabled
// for the tunnel-mode operator. A non-empty value still satisfies
// Grafana's startup contract.
const grafanaAnonPassword = "nvoi-tunnel-only" //nolint:gosec // not a real secret in this mode

// buildGrafanaAdminSecret renders the admin-password Secret. In
// tunnel-only mode (rt.Monitor.AdminPassword == "") we still emit
// the Secret with the tunnel-only sentinel so Grafana's env-from-Secret
// reference always resolves — no conditional env wiring on the
// Deployment side.
func buildGrafanaAdminSecret(rt *runtime.Runtime) *corev1.Secret {
	pwd := grafanaAnonPassword
	if rt.Monitor != nil && rt.Monitor.AdminPassword != "" {
		pwd = rt.Monitor.AdminPassword
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      grafanaAdminSecretName,
			Namespace: Namespace,
			Labels:    objectLabels(grafanaComponent),
		},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"admin-password": pwd},
	}
}

// buildGrafanaConfigMap renders grafana.ini. Differs by access mode:
//   - tunnel-only (domain == ""): anonymous viewer enabled.
//   - public (domain set):        anonymous disabled; admin login
//                                  required; server.root_url set so
//                                  Grafana emits correct callback
//                                  URLs.
func buildGrafanaConfigMap(rt *runtime.Runtime) *corev1.ConfigMap {
	domain := ""
	if rt.Monitor != nil {
		domain = rt.Monitor.Domain
	}

	var ini string
	if domain != "" {
		ini = `[server]
root_url = https://` + domain + `/
serve_from_sub_path = false

[auth.anonymous]
enabled = false

[security]
allow_embedding = false
`
	} else {
		ini = `[server]
root_url = http://localhost:3000/

[auth.anonymous]
enabled = true
org_role = Viewer

[security]
allow_embedding = false
`
	}
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
						// Dashboards: an emptyDir the sidecar writes JSON
						// files into + a projected provider-config layer.
						// Use emptyDir as base for sidecar writes; provider
						// config goes into a sibling ConfigMap mount via
						// subPath (separate volume entry would conflict on
						// the same mountPath; the sidecar handles the
						// provider auto-create in kiwigrid 1.27+).
						{Name: "dashboards", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
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
	grafanaDatasourcesName   = "grafana-datasources"
	grafanaContactPointsName = "grafana-contact-points"
	grafanaPolicyName        = "grafana-notification-policy"
)

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
