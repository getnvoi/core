package kube

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecRequest is the input to (*Client).Exec.
type ExecRequest struct {
	Namespace string
	Pod       string
	Container string // empty = first container in pod
	Command   []string
	Stdin     io.Reader // nil = no stdin
	Stdout    io.Writer // nil = discard
	Stderr    io.Writer // nil = discard
	TTY       bool
}

// Exec runs a command inside a pod via the apiserver's exec
// subresource. Streams over the same SSH-tunneled rest.Config the
// rest of the typed client uses — no shell-out, no kubectl wrapper,
// no shell quoting on stdin (it's a separate stream).
//
// Tests inject c.ExecFunc to capture the request and return canned
// output without a real apiserver. The hook bypasses SPDY entirely
// and is the only way to test Caddy admin reload / cert wait probes
// without standing up a kind cluster.
func (c *Client) Exec(ctx context.Context, req ExecRequest) error {
	if c.ExecFunc != nil {
		return c.ExecFunc(ctx, req)
	}
	if c.cfg == nil {
		return fmt.Errorf("kube exec: client has no rest.Config (test path missing ExecFunc?)")
	}
	if req.Stdout == nil {
		req.Stdout = io.Discard
	}
	if req.Stderr == nil {
		req.Stderr = io.Discard
	}

	restReq := c.CS.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(req.Namespace).
		Name(req.Pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: req.Container,
			Command:   req.Command,
			Stdin:     req.Stdin != nil,
			Stdout:    true,
			Stderr:    true,
			TTY:       req.TTY,
		}, scheme.ParameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(c.cfg, "POST", restReq.URL())
	if err != nil {
		return fmt.Errorf("build exec: %w", err)
	}
	return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  req.Stdin,
		Stdout: req.Stdout,
		Stderr: req.Stderr,
		Tty:    req.TTY,
	})
}

// FirstPod returns the name of the first pod in `ns` carrying
// `app.kubernetes.io/name=<service>`. Errors when no pod exists yet —
// the caller can decide whether that's a hard error (Caddy reload
// requires a running pod) or a soft signal (`describe` falling back
// to "no routes loaded").
func (c *Client) FirstPod(ctx context.Context, ns, service string) (string, error) {
	pods, err := c.CS.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "app.kubernetes.io/name=" + service,
	})
	if err != nil {
		return "", fmt.Errorf("list pods for %s: %w", service, err)
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found for service %q", service)
	}
	return pods.Items[0].Name, nil
}

// GetServicePort returns the first port of a Service. Used by the
// ingress reconcile to resolve the backend port for Caddy's
// reverse_proxy upstream.
func (c *Client) GetServicePort(ctx context.Context, ns, name string) (int, error) {
	svc, err := c.CS.CoreV1().Services(ns).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return 0, fmt.Errorf("service %q not found in %s", name, ns)
	}
	if err != nil {
		return 0, fmt.Errorf("get service %s: %w", name, err)
	}
	if len(svc.Spec.Ports) == 0 {
		return 0, fmt.Errorf("service %q has no ports", name)
	}
	return int(svc.Spec.Ports[0].Port), nil
}
