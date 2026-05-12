package kube

// portforward.go provides kc.PortForward — local-port → in-cluster pod
// port forwarding through the existing SSH-tunneled apiserver. Used
// by `nvoi monitor` to expose Grafana on the operator's localhost
// without publishing a public endpoint.
//
// Same SSH tunnel as every other kube API call: client-go's portforward
// rides client-go's SPDY-over-HTTPS, which rides cfg.Host (our local
// listener), which rides ssh.DialTCP to the apiserver. Zero new network
// paths.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// PortForward forwards localPort on the operator's machine to
// remotePort on a Pod backing svcName in ns. Resolves the Service to
// one of its Ready Pods, then opens a SPDY port-forward through the
// apiserver subresource.
//
// Blocks until ctx is cancelled or the forward errors. Returns
// ctx.Err() on cancellation (clean ctrl-C unwind in the caller).
func (c *Client) PortForward(ctx context.Context, ns, svcName string, localPort, remotePort int) error {
	if c.cfg == nil {
		return fmt.Errorf("PortForward: client has no rest.Config (NewForTest path)")
	}

	podName, err := c.firstReadyPodFor(ctx, ns, svcName)
	if err != nil {
		return fmt.Errorf("resolve pod for %s/%s: %w", ns, svcName, err)
	}

	// Build the apiserver URL for the portforward subresource on the
	// chosen Pod. RESTClient gives us the right base URL + auth
	// already wired to the tunnel.
	req := c.CS.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(ns).
		Name(podName).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(c.cfg)
	if err != nil {
		return fmt.Errorf("spdy roundtripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader,
		&http.Client{Transport: transport},
		http.MethodPost,
		&url.URL{Scheme: "https", Path: req.URL().Path, Host: req.URL().Host})

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	defer close(stopCh)

	fw, err := portforward.New(dialer,
		[]string{fmt.Sprintf("%d:%d", localPort, remotePort)},
		stopCh, readyCh,
		io.Discard, io.Discard)
	if err != nil {
		return fmt.Errorf("create port-forwarder: %w", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- fw.ForwardPorts() }()

	// Wait for either the forwarder to fail outright or to signal
	// ready. After ready, block on ctx cancellation OR a late error.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-readyCh:
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errCh:
			return err
		}
	}
}

// firstReadyPodFor returns the name of a Ready Pod backing svcName.
// Reads svc.Spec.Selector and lists matching pods, filtering by the
// PodReady condition.
//
// Error when no pods match OR none are Ready — surfaces the wait-
// for-rollout state cleanly ("grafana not yet Ready, try again in
// 10s") rather than dialing an unready pod and getting connection
// refused.
func (c *Client) firstReadyPodFor(ctx context.Context, ns, svcName string) (string, error) {
	svc, err := c.CS.CoreV1().Services(ns).Get(ctx, svcName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if len(svc.Spec.Selector) == 0 {
		return "", fmt.Errorf("service %s/%s has no selector", ns, svcName)
	}

	sel := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: svc.Spec.Selector})
	pods, err := c.CS.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel})
	if err != nil {
		return "", err
	}

	for _, p := range pods.Items {
		if isPodReady(p) {
			return p.Name, nil
		}
	}
	return "", fmt.Errorf("no Ready pod for service %s/%s", ns, svcName)
}

func isPodReady(p corev1.Pod) bool {
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
