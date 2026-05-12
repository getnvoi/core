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
// Single Deployment replica. Two containers:
//   - grafana: official image, reads provisioning dirs at boot AND
//     watches them for hot-reload (the sidecar writes new files
//     after startup).
//   - k8s-sidecar: watches the namespace for ConfigMaps labeled
//     grafana_dashboard=1 / grafana_datasource=1 / grafana_alert=1,
//     drops their content into the matching provisioning dir.
//
// Shared emptyDir for /etc/grafana/provisioning so both containers
// see the same files — sidecar writes, grafana reads.
func buildGrafanaDeployment() *appsv1.Deployment {
	replicas := int32(1)
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
							Env: []corev1.EnvVar{
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
							},
							Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 3000}},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config", MountPath: "/etc/grafana/grafana.ini", SubPath: "grafana.ini"},
								{Name: "provisioning", MountPath: "/etc/grafana/provisioning"},
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
								// Multi-label support: kiwigrid sidecar runs
								// a SINGLE label-filter per container.
								// For multi-resource provisioning (datasources,
								// dashboards, alerts) we'd run multiple sidecar
								// containers OR rely on the sidecar's secondary
								// label discovery via LABEL2 (1.x+). For v1,
								// dashboards via this container; datasources +
								// alerts are applied directly as files written
								// to a startup ConfigMap mount (see 4e + PR 5).
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "provisioning", MountPath: "/etc/grafana/provisioning"},
							},
							Resources: stdRequests("50m", "64Mi"),
						},
					},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{
							ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: grafanaConfigName}},
						}},
						{Name: "provisioning", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
					},
				},
			},
		},
	}
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
