package kubevip_test

import (
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/internal/kubevip"
)

func TestManifest_RendersAllRequiredFields(t *testing.T) {
	out, err := kubevip.Manifest(kubevip.ManifestSpec{
		VIP:       "10.0.1.250",
		Interface: "eth0",
	})
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	s := string(out)

	for _, want := range []string{
		// Image pinned (catches accidental "latest" / drift).
		"image: ghcr.io/kube-vip/kube-vip:" + kubevip.Version,
		// ARP mode + leader election — these two switches MUST be
		// true for our topology. A regression that flips either
		// breaks failover semantics.
		`name: vip_arp,            value: "true"`,
		`name: vip_leaderelection, value: "true"`,
		// Control-plane mode (we don't use kube-vip's Service LB
		// surface).
		`name: cp_enable,          value: "true"`,
		`name: svc_enable,         value: "false"`,
		// VIP + interface threaded through.
		`name: address,            value: "10.0.1.250"`,
		`name: vip_interface,      value: "eth0"`,
		// Pod-level requirements for ARP.
		"hostNetwork: true",
		`add: ["NET_ADMIN", "NET_RAW"]`,
		// k3s kubeconfig mount — kube-vip talks to the local
		// apiserver via this path.
		"path: /etc/rancher/k3s/k3s.yaml",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("manifest missing %q\n--- output ---\n%s", want, s)
		}
	}
}

func TestManifest_MissingVIP_Errors(t *testing.T) {
	if _, err := kubevip.Manifest(kubevip.ManifestSpec{Interface: "eth0"}); err == nil {
		t.Error("expected error when VIP empty")
	}
}

func TestManifest_MissingInterface_Errors(t *testing.T) {
	if _, err := kubevip.Manifest(kubevip.ManifestSpec{VIP: "10.0.1.250"}); err == nil {
		t.Error("expected error when Interface empty")
	}
}

func TestRBAC_DeclaresRequiredVerbs(t *testing.T) {
	s := string(kubevip.RBAC())
	// Each Kubernetes API surface kube-vip touches gets verb-level
	// assertions. Catches accidental rule deletions on PR review.
	for _, want := range []string{
		"kind: ServiceAccount",
		"name: kube-vip",
		"namespace: kube-system",
		"kind: ClusterRole",
		"kind: ClusterRoleBinding",
		// Leader election uses Leases — failover semantics depend on
		// this verb set.
		`apiGroups: ["coordination.k8s.io"]`,
		// EndpointSlices are how kube-vip discovers service backends
		// (even though we don't use svc_enable, the discovery client
		// still watches them).
		`apiGroups: ["discovery.k8s.io"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("RBAC missing %q\n--- output ---\n%s", want, s)
		}
	}
}
