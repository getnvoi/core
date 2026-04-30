package runner

import (
	"reflect"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

// del / create / replace are tiny helpers for building ResourceChange
// fixtures inline without ceremony.
func change(actions ...tfjson.Action) *tfjson.Change {
	return &tfjson.Change{Actions: actions}
}

func rc(typ, name string, c *tfjson.Change) *tfjson.ResourceChange {
	return &tfjson.ResourceChange{Type: typ, Name: name, Change: c}
}

func TestPlanNodeDestroys(t *testing.T) {
	cases := []struct {
		name string
		plan *tfjson.Plan
		want []string
	}{
		{
			name: "no changes",
			plan: &tfjson.Plan{ResourceChanges: nil},
			want: nil,
		},
		{
			name: "creates only — no destroys",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				rc("hcloud_server", "master", change(tfjson.ActionCreate)),
				rc("hcloud_network", "default", change(tfjson.ActionCreate)),
			}},
			want: nil,
		},
		{
			name: "single server destroy — caught",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				rc("hcloud_server", "worker-2", change(tfjson.ActionDelete)),
			}},
			want: []string{"worker-2"},
		},
		{
			name: "non-server destroys ignored (network, firewall, lb)",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				rc("hcloud_server", "worker-1", change(tfjson.ActionDelete)),
				rc("hcloud_network", "default", change(tfjson.ActionDelete)),
				rc("hcloud_firewall", "default", change(tfjson.ActionDelete)),
				rc("hcloud_load_balancer", "cp", change(tfjson.ActionDelete)),
			}},
			want: []string{"worker-1"},
		},
		{
			name: "replace counts as destroy (delete+create on same resource)",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				rc("hcloud_server", "master-2", change(tfjson.ActionDelete, tfjson.ActionCreate)),
			}},
			want: []string{"master-2"},
		},
		{
			name: "multiple destroys sorted",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				rc("hcloud_server", "worker-2", change(tfjson.ActionDelete)),
				rc("hcloud_server", "master-3", change(tfjson.ActionDelete)),
				rc("hcloud_server", "worker-1", change(tfjson.ActionDelete)),
			}},
			want: []string{"master-3", "worker-1", "worker-2"},
		},
		{
			name: "nil Change is skipped (defensive)",
			plan: &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
				rc("hcloud_server", "x", nil),
				rc("hcloud_server", "y", change(tfjson.ActionDelete)),
			}},
			want: []string{"y"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := planTypeDestroys(tc.plan, "hcloud_server")
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

// Asserts the same walker (planTypeDestroys) is re-used for both
// node-resource and tunnel-resource filtering — the type filter is
// the only behavioural axis. Locks the contract that adding a new
// plan-gated drain target is one constant + one call site, not a
// fork of the walker.
func TestPlanTypeDestroys_TunnelFilter(t *testing.T) {
	plan := &tfjson.Plan{ResourceChanges: []*tfjson.ResourceChange{
		// Tunnel going away — caught.
		rc("cloudflare_zero_trust_tunnel_cloudflared", "main", change(tfjson.ActionDelete)),
		// Tunnel-config going away — NOT caught (only the tunnel
		// object itself triggers the drain).
		rc("cloudflare_zero_trust_tunnel_cloudflared_config", "main", change(tfjson.ActionDelete)),
		// Server going away — NOT caught when filter is tunnel.
		rc("hcloud_server", "master", change(tfjson.ActionDelete)),
		// Tunnel being replaced — caught (delete leg of replace).
		rc("cloudflare_zero_trust_tunnel_cloudflared", "rotated", change(tfjson.ActionDelete, tfjson.ActionCreate)),
		// Tunnel being created (no destroy) — NOT caught.
		rc("cloudflare_zero_trust_tunnel_cloudflared", "new", change(tfjson.ActionCreate)),
	}}

	got := planTypeDestroys(plan, "cloudflare_zero_trust_tunnel_cloudflared")
	want := []string{"main", "rotated"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}
