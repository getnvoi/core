package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Caddy poll/timeout knobs — overridable via SetCaddyTimingForTest.
// Production values bracket Let's Encrypt's typical ACME HTTP-01
// latency (issuance is usually 5–30s; we wait up to 10 minutes to
// absorb DNS propagation lag and LE rate-limit retries).
var (
	caddyPollInterval = 3 * time.Second
	caddyCertTimeout  = 10 * time.Minute
	caddyHTTPSTimeout = 5 * time.Minute
	caddyReadyTimeout = 2 * time.Minute
)

// SetCaddyTimingForTest collapses every Caddy poll loop to fast
// intervals for tests. Returns a cleanup that restores production
// values.
func SetCaddyTimingForTest(poll, total time.Duration) func() {
	op, oc, oh, or := caddyPollInterval, caddyCertTimeout, caddyHTTPSTimeout, caddyReadyTimeout
	caddyPollInterval, caddyCertTimeout, caddyHTTPSTimeout, caddyReadyTimeout = poll, total, total, total
	return func() {
		caddyPollInterval, caddyCertTimeout, caddyHTTPSTimeout, caddyReadyTimeout = op, oc, oh, or
	}
}

// EnsureCaddy idempotently applies the four Caddy resources (PVC,
// ConfigMap, Service, Deployment) in kube-system with owner=caddy,
// then waits for the Deployment to become Available. Reapplied
// every reconcile → zero drift.
func (c *Client) EnsureCaddy(ctx context.Context) error {
	if err := c.ApplyOwned(ctx, CaddyNamespace, OwnerCaddy, buildCaddyPVC()); err != nil {
		return fmt.Errorf("ensure caddy pvc: %w", err)
	}
	if err := c.ApplyOwned(ctx, CaddyNamespace, OwnerCaddy, buildCaddyConfigMap()); err != nil {
		return fmt.Errorf("ensure caddy configmap: %w", err)
	}
	if err := c.ApplyOwned(ctx, CaddyNamespace, OwnerCaddy, buildCaddyService()); err != nil {
		return fmt.Errorf("ensure caddy service: %w", err)
	}
	if err := c.ApplyOwned(ctx, CaddyNamespace, OwnerCaddy, buildCaddyDeployment()); err != nil {
		return fmt.Errorf("ensure caddy deployment: %w", err)
	}
	return c.waitForCaddyReady(ctx)
}

// waitForCaddyReady polls until the Caddy Deployment's ReadyReplicas
// equals Spec.Replicas. Caps at caddyReadyTimeout (2 minutes prod).
func (c *Client) waitForCaddyReady(ctx context.Context) error {
	deadline := time.Now().Add(caddyReadyTimeout)
	for time.Now().Before(deadline) {
		dep, err := c.CS.AppsV1().Deployments(CaddyNamespace).Get(ctx, CaddyName, metav1.GetOptions{})
		if err == nil && dep.Spec.Replicas != nil && dep.Status.ReadyReplicas >= *dep.Spec.Replicas && dep.Status.ObservedGeneration >= dep.Generation {
			return nil
		}
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("wait caddy ready: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(caddyPollInterval):
		}
	}
	return fmt.Errorf("caddy deployment not ready within %s", caddyReadyTimeout)
}

// ReloadCaddyConfig POSTs configJSON to Caddy's admin API at
// localhost:2019/load via Exec into the Caddy pod. Caddy validates
// first; on success the listeners atomically swap to the new routes
// without dropping live connections. On rejection, Caddy's error
// body is captured and surfaced verbatim.
func (c *Client) ReloadCaddyConfig(ctx context.Context, configJSON []byte) error {
	pod, err := c.firstCaddyPod(ctx)
	if err != nil {
		return fmt.Errorf("caddy reload: %w", err)
	}
	var stdout, stderr bytes.Buffer
	execErr := c.Exec(ctx, ExecRequest{
		Namespace: CaddyNamespace,
		Pod:       pod,
		Container: CaddyName,
		Command: []string{"sh", "-c",
			"curl --fail-with-body -sS -X POST --data-binary @- " +
				"-H 'Content-Type: application/json' " +
				"http://" + CaddyAdminListen + "/load",
		},
		Stdin:  bytes.NewReader(configJSON),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if execErr != nil {
		body := strings.TrimSpace(stdout.String())
		errOut := strings.TrimSpace(stderr.String())
		switch {
		case body != "":
			return fmt.Errorf("caddy reload rejected: %s", body)
		case errOut != "":
			return fmt.Errorf("caddy reload: %s: %w", errOut, execErr)
		default:
			return fmt.Errorf("caddy reload: %w", execErr)
		}
	}
	return nil
}

// WaitForCaddyCert polls the Caddy pod until the ACME cert file for
// `domain` exists on /data with non-zero size. Indicates Caddy
// completed the ACME HTTP-01 flow and persisted the cert chain.
//
// Path:
//
//	/data/caddy/certificates/acme-v02.api.letsencrypt.org-directory/<domain>/<domain>.crt
//
// Caller treats timeout as warn-and-move-on — next deploy re-verifies,
// Caddy keeps retrying ACME between deploys regardless.
func (c *Client) WaitForCaddyCert(ctx context.Context, domain string) error {
	pod, err := c.firstCaddyPod(ctx)
	if err != nil {
		return err
	}
	certPath := fmt.Sprintf(
		"%s/caddy/certificates/acme-v02.api.letsencrypt.org-directory/%s/%s.crt",
		CaddyDataDir, domain, domain,
	)
	cmd := []string{"sh", "-c", "test -s '" + certPath + "'"}
	return c.pollExec(ctx, pod, cmd, caddyCertTimeout)
}

// WaitForCaddyHTTPS curls https://<domain><healthPath> from inside
// the Caddy pod and waits for any non-5xx response. Run from the pod
// (not the operator's box) so propagation isn't gated on the
// operator's local DNS — the pod resolves through the same
// upstream DNS the public uses.
//
// 401/403 = success (app is up, just rejecting unauthenticated probes).
// Connection failures (curl exits 000) and 5xx → retry.
func (c *Client) WaitForCaddyHTTPS(ctx context.Context, domain, healthPath string) error {
	pod, err := c.firstCaddyPod(ctx)
	if err != nil {
		return err
	}
	if healthPath == "" {
		healthPath = "/"
	}
	url := fmt.Sprintf("https://%s%s", domain, healthPath)
	script := fmt.Sprintf(
		`code=$(curl -s --connect-timeout 5 -o /dev/null -w '%%{http_code}' '%s'); `+
			`case "$code" in 5*|0*) exit 1 ;; *) exit 0 ;; esac`,
		url,
	)
	return c.pollExec(ctx, pod, []string{"sh", "-c", script}, caddyHTTPSTimeout)
}

// pollExec runs `cmd` in `pod` on a fixed interval, succeeding when
// the command exits 0. Times out per `total`. Used by both cert and
// HTTPS probes — same loop, different commands.
func (c *Client) pollExec(ctx context.Context, pod string, cmd []string, total time.Duration) error {
	deadline := time.Now().Add(total)
	for time.Now().Before(deadline) {
		err := c.Exec(ctx, ExecRequest{
			Namespace: CaddyNamespace,
			Pod:       pod,
			Container: CaddyName,
			Command:   cmd,
			Stdout:    io.Discard,
			Stderr:    io.Discard,
		})
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(caddyPollInterval):
		}
	}
	return fmt.Errorf("poll timeout after %s", total)
}

// firstCaddyPod returns the running Caddy pod's name. Errors with a
// clear message if the pod isn't up yet so the reconciler surfaces
// "caddy not ready" instead of "no pods found."
func (c *Client) firstCaddyPod(ctx context.Context) (string, error) {
	name, err := c.FirstPod(ctx, CaddyNamespace, CaddyName)
	if err != nil {
		return "", fmt.Errorf("caddy pod not ready: %w", err)
	}
	return name, nil
}
