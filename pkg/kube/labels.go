package kube

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// LabelNode applies (or overwrites) the given label on the named
// Node. Used by the deploy pipeline to stamp `nvoi-role=<yaml-key>`
// on every node so workloads' nodeSelector / nodeAffinity (which
// upstream nvoi keys on this exact label) match correctly.
//
// Strategic-merge-patch is the right tool: atomic, takes a JSON
// fragment that only overrides the listed labels (existing labels
// untouched), no Get/Update race so no need for retry-on-conflict.
//
// Idempotent — repeated calls with the same key/value are no-ops on
// the apiserver side.
func (c *Client) LabelNode(ctx context.Context, name, key, value string) error {
	patch, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{key: value},
		},
	})
	if err != nil {
		return fmt.Errorf("encode label patch: %w", err)
	}
	_, err = c.CS.CoreV1().Nodes().Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{FieldManager: FieldManager})
	if err != nil {
		return fmt.Errorf("patch node %s label %s=%s: %w", name, key, value, err)
	}
	return nil
}
