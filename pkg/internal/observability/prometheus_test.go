package observability

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// fixtureCreds is the canonical fake bucket-creds bundle every
// manifest-shape test uses. Values are recognizable in golden strings.
func fixtureCreds() BucketCreds {
	return BucketCreds{
		Endpoint:      "https://acct.r2.cloudflarestorage.com",
		Region:        "auto",
		AccessKey:     "AKIAFAKE",
		SecretKey:     "SECRETFAKE",
		LogsBucket:    "nvoi-hello-dev-logs",
		MetricsBucket: "nvoi-hello-dev-metrics",
	}
}

func TestBuildThanosObjstoreSecret(t *testing.T) {
	s := buildThanosObjstoreSecret(fixtureCreds())
	if s.Namespace != Namespace {
		t.Errorf("namespace: %q", s.Namespace)
	}
	if s.Name != objstoreSecretName {
		t.Errorf("name: %q", s.Name)
	}
	yml, ok := s.StringData["objstore.yml"]
	if !ok {
		t.Fatal("objstore.yml key missing")
	}
	// Wire format sanity — type, bucket, host stripped of scheme,
	// credentials passed through.
	for _, want := range []string{
		"type: S3",
		"bucket: nvoi-hello-dev-metrics",
		"endpoint: acct.r2.cloudflarestorage.com", // scheme stripped
		"region: auto",
		"access_key: AKIAFAKE",
		"secret_key: SECRETFAKE",
	} {
		if !strings.Contains(yml, want) {
			t.Errorf("objstore.yml missing %q\n%s", want, yml)
		}
	}
}

func TestBuildPrometheusStatefulSet_ContainersAndMounts(t *testing.T) {
	ss := buildPrometheusStatefulSet()
	if ss.Namespace != Namespace {
		t.Errorf("namespace: %q", ss.Namespace)
	}
	if got := len(ss.Spec.Template.Spec.Containers); got != 2 {
		t.Fatalf("want 2 containers (prom + thanos sidecar), got %d", got)
	}
	prom := ss.Spec.Template.Spec.Containers[0]
	sidecar := ss.Spec.Template.Spec.Containers[1]
	if prom.Name != "prometheus" {
		t.Errorf("container[0] name: %q", prom.Name)
	}
	if sidecar.Name != "thanos-sidecar" {
		t.Errorf("container[1] name: %q", sidecar.Name)
	}

	// Block-duration args are the sidecar contract — losing them
	// silently breaks Thanos.
	mustArg(t, prom.Args, "--storage.tsdb.min-block-duration=2h")
	mustArg(t, prom.Args, "--storage.tsdb.max-block-duration=2h")

	// Shared TSDB mount between containers — sidecar reads what prom
	// writes.
	if !hasMount(prom.VolumeMounts, "tsdb", "/prometheus") {
		t.Errorf("prom missing /prometheus mount: %v", prom.VolumeMounts)
	}
	if !hasMount(sidecar.VolumeMounts, "tsdb", "/prometheus") {
		t.Errorf("sidecar missing /prometheus mount: %v", sidecar.VolumeMounts)
	}
	if !hasMount(sidecar.VolumeMounts, "objstore", "/etc/thanos") {
		t.Errorf("sidecar missing objstore mount: %v", sidecar.VolumeMounts)
	}

	// emptyDir tsdb (NOT PVC) — chunks ship to bucket, in-pod state
	// is ephemeral.
	if vol := findVolume(ss.Spec.Template.Spec.Volumes, "tsdb"); vol == nil || vol.EmptyDir == nil {
		t.Errorf("tsdb volume should be emptyDir: %+v", vol)
	}
	if vol := findVolume(ss.Spec.Template.Spec.Volumes, "objstore"); vol == nil || vol.Secret == nil ||
		vol.Secret.SecretName != objstoreSecretName {
		t.Errorf("objstore volume should reference Secret %q: %+v", objstoreSecretName, vol)
	}
}

func TestBuildPrometheusServices_HeadlessAndClusterIP(t *testing.T) {
	svcs := buildPrometheusServices()
	if len(svcs) != 2 {
		t.Fatalf("want 2 services (headless + clusterip), got %d", len(svcs))
	}
	headless, clusterIP := svcs[0], svcs[1]

	if headless.Spec.ClusterIP != corev1.ClusterIPNone {
		t.Errorf("first service should be headless (ClusterIP=None), got %q", headless.Spec.ClusterIP)
	}
	if clusterIP.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("second service should be ClusterIP, got %q", clusterIP.Spec.Type)
	}
	if clusterIP.Spec.ClusterIP == corev1.ClusterIPNone {
		t.Errorf("clusterIP service must NOT be headless")
	}

	// Both expose the same pair of ports.
	for _, s := range svcs {
		if !hasPort(s.Spec.Ports, 9090) {
			t.Errorf("%s missing port 9090", s.Name)
		}
		if !hasPort(s.Spec.Ports, 10901) {
			t.Errorf("%s missing port 10901 (thanos grpc)", s.Name)
		}
	}
}

func TestBuildThanosQuerier_TargetsHeadlessSidecarAndStore(t *testing.T) {
	dep, svc := buildThanosQuerier()
	if dep.Namespace != Namespace || svc.Namespace != Namespace {
		t.Errorf("namespace mismatch")
	}
	args := dep.Spec.Template.Spec.Containers[0].Args
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, prometheusComponent+"-headless") {
		t.Errorf("querier should target prometheus headless for sidecar discovery: %v", args)
	}
	if !strings.Contains(joined, thanosStoreComponent) {
		t.Errorf("querier should target thanos-store for historical reads: %v", args)
	}
	if !hasPort(svc.Spec.Ports, 9090) {
		t.Errorf("querier service must expose :9090 (Grafana datasource target)")
	}
}

func TestBuildThanosStore_MountsObjstoreSecret(t *testing.T) {
	dep, svc := buildThanosStore()
	c := dep.Spec.Template.Spec.Containers[0]
	if !hasMount(c.VolumeMounts, "objstore", "/etc/thanos") {
		t.Errorf("store missing objstore mount: %v", c.VolumeMounts)
	}
	if vol := findVolume(dep.Spec.Template.Spec.Volumes, "objstore"); vol == nil ||
		vol.Secret == nil || vol.Secret.SecretName != objstoreSecretName {
		t.Errorf("store objstore volume should reference Secret %q: %+v", objstoreSecretName, vol)
	}
	if !hasPort(svc.Spec.Ports, 10901) {
		t.Errorf("store service must expose :10901 (gRPC)")
	}
}

func TestStripScheme(t *testing.T) {
	for in, want := range map[string]string{
		"https://acct.r2.cloudflarestorage.com":  "acct.r2.cloudflarestorage.com",
		"http://localhost:9000":                  "localhost:9000",
		"https://acct.r2.cloudflarestorage.com/": "acct.r2.cloudflarestorage.com",
		"bare-host":                              "bare-host",
	} {
		if got := stripScheme(in); got != want {
			t.Errorf("stripScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

// ── helpers ───────────────────────────────────────────────────────────

func mustArg(t *testing.T, args []string, want string) {
	t.Helper()
	for _, a := range args {
		if a == want {
			return
		}
	}
	t.Errorf("expected arg %q in %v", want, args)
}

func hasMount(mounts []corev1.VolumeMount, name, path string) bool {
	for _, m := range mounts {
		if m.Name == name && m.MountPath == path {
			return true
		}
	}
	return false
}

func findVolume(vols []corev1.Volume, name string) *corev1.Volume {
	for i := range vols {
		if vols[i].Name == name {
			return &vols[i]
		}
	}
	return nil
}

func hasPort(ports []corev1.ServicePort, port int32) bool {
	for _, p := range ports {
		if p.Port == port {
			return true
		}
	}
	return false
}
