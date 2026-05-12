package observability

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestBuildLokiConfigMap_S3Wiring(t *testing.T) {
	cm := buildLokiConfigMap(fixtureCreds())
	if cm.Namespace != Namespace {
		t.Errorf("namespace: %q", cm.Namespace)
	}
	yml := cm.Data["config.yaml"]
	for _, want := range []string{
		"s3://AKIAFAKE:SECRETFAKE@acct.r2.cloudflarestorage.com/nvoi-hello-dev-logs",
		"region: auto",
		"s3forcepathstyle: true",
		"store: tsdb",
	} {
		if !strings.Contains(yml, want) {
			t.Errorf("loki config.yaml missing %q\n%s", want, yml)
		}
	}
}

func TestBuildLoki_StatefulSetAndService(t *testing.T) {
	ss, svc := buildLoki()
	if ss.Namespace != Namespace || svc.Namespace != Namespace {
		t.Errorf("namespace mismatch")
	}
	c := ss.Spec.Template.Spec.Containers[0]
	if c.Name != "loki" {
		t.Errorf("container name: %q", c.Name)
	}
	mustArg(t, c.Args, "-config.file=/etc/loki/config.yaml")
	mustArg(t, c.Args, "-target=all")
	if !hasMount(c.VolumeMounts, "config", "/etc/loki") {
		t.Errorf("missing /etc/loki mount: %v", c.VolumeMounts)
	}
	if !hasMount(c.VolumeMounts, "data", "/loki") {
		t.Errorf("missing /loki mount: %v", c.VolumeMounts)
	}
	if !hasPort(svc.Spec.Ports, 3100) {
		t.Errorf("loki service must expose :3100 (Grafana datasource target)")
	}
}

func TestBuildPromtailRBAC_ReadOnlyNodeDiscovery(t *testing.T) {
	sa, cr, crb := buildPromtailRBAC()
	if sa.Namespace != Namespace {
		t.Errorf("SA namespace: %q", sa.Namespace)
	}
	if len(cr.Rules) != 1 {
		t.Fatalf("ClusterRole should have 1 rule, got %d", len(cr.Rules))
	}
	verbs := cr.Rules[0].Verbs
	for _, want := range []string{"get", "list", "watch"} {
		if !contains(verbs, want) {
			t.Errorf("missing verb %q in %v", want, verbs)
		}
	}
	// Mutating verbs MUST be absent — Promtail is a reader only.
	for _, forbidden := range []string{"create", "update", "delete", "patch"} {
		if contains(verbs, forbidden) {
			t.Errorf("forbidden mutating verb %q present: %v", forbidden, verbs)
		}
	}
	// Binding wires SA → role.
	if crb.RoleRef.Name != "nvoi-"+promtailComponent {
		t.Errorf("CRB roleRef wrong: %q", crb.RoleRef.Name)
	}
	if len(crb.Subjects) != 1 || crb.Subjects[0].Name != promtailComponent {
		t.Errorf("CRB subjects wrong: %+v", crb.Subjects)
	}
}

func TestBuildPromtailDaemonSet_TolerationsAndMounts(t *testing.T) {
	ds := buildPromtailDaemonSet()
	if ds.Namespace != Namespace {
		t.Errorf("namespace: %q", ds.Namespace)
	}
	// Toleration for ALL taints — promtail must schedule on master too
	// so master-pinned services have their logs shipped.
	if len(ds.Spec.Template.Spec.Tolerations) == 0 {
		t.Fatal("no tolerations — promtail won't schedule on tainted master")
	}
	if ds.Spec.Template.Spec.Tolerations[0].Operator != corev1.TolerationOpExists {
		t.Errorf("expected Exists toleration, got %+v", ds.Spec.Template.Spec.Tolerations[0])
	}
	c := ds.Spec.Template.Spec.Containers[0]
	if !hasMount(c.VolumeMounts, "varlog", "/var/log") {
		t.Errorf("missing /var/log host mount: %v", c.VolumeMounts)
	}
	for _, m := range c.VolumeMounts {
		if m.Name == "varlog" && !m.ReadOnly {
			t.Error("varlog mount must be ReadOnly")
		}
	}
	if ds.Spec.Template.Spec.ServiceAccountName != promtailComponent {
		t.Errorf("SA name: %q", ds.Spec.Template.Spec.ServiceAccountName)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
