package kube

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EnsureSecret creates the Secret if missing or merges the given keys
// into it otherwise. Other keys not in `kvs` are left untouched —
// callers that want full-replacement use ApplyOwned with a fully-
// formed *corev1.Secret.
//
// `owner` is one of the OwnerXxx constants in owned.go — same
// discriminator ApplyOwned uses. Stamped on Create AND on Update so
// labels stay in lockstep with the rest of the reconciler.
//
// This is the discovery contract: SweepOwned, describe, list-by-owner
// tooling sees every nvoi-owned Secret via the standard
// nvoi/owner=<owner> selector without per-creation-site label rituals.
func (c *Client) EnsureSecret(ctx context.Context, ns, owner, name string, kvs map[string]string) error {
	if c == nil {
		return fmt.Errorf("kube client not initialized")
	}
	if owner == "" {
		return fmt.Errorf("EnsureSecret: owner required")
	}
	existing, err := c.CS.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		// Use Data (bytes), not StringData — the apiserver converts
		// StringData→Data server-side, but client-go fakes don't, and
		// downstream reads go through Data. Writing Data directly keeps
		// real + fake behavior identical.
		data := make(map[string][]byte, len(kvs))
		for k, v := range kvs {
			data[k] = []byte(v)
		}
		secret := &corev1.Secret{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ns,
				Labels:    map[string]string{LabelOwner: owner},
			},
			Type: corev1.SecretTypeOpaque,
			Data: data,
		}
		_, err := c.CS.CoreV1().Secrets(ns).Create(ctx, secret, metav1.CreateOptions{FieldManager: FieldManager})
		if err != nil {
			return fmt.Errorf("create secret %s/%s: %w", ns, name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get secret %s/%s: %w", ns, name, err)
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}
	for k, v := range kvs {
		existing.Data[k] = []byte(v)
	}
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	existing.Labels[LabelOwner] = owner
	_, err = c.CS.CoreV1().Secrets(ns).Update(ctx, existing, metav1.UpdateOptions{FieldManager: FieldManager})
	if err != nil {
		return fmt.Errorf("update secret %s/%s: %w", ns, name, err)
	}
	return nil
}

// GetSecretValue returns the decoded value of a single key in a
// Secret. secret.Data is already []byte (base64-decoded by the API
// server).
func (c *Client) GetSecretValue(ctx context.Context, ns, name, key string) (string, error) {
	secret, err := c.CS.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", fmt.Errorf("secret %s/%s not found", ns, name)
	}
	if err != nil {
		return "", fmt.Errorf("get secret %s/%s: %w", ns, name, err)
	}
	val, ok := secret.Data[key]
	if !ok || len(val) == 0 {
		return "", fmt.Errorf("secret key %q not found or empty", key)
	}
	return string(val), nil
}
