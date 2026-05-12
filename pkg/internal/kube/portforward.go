package kube

// portforward.go provides kc.PortForward — local-port → in-cluster pod
// port forwarding. Implementation rides RAW TCP through SSH (same
// pattern as openTunnel in client.go for the apiserver tunnel), NOT
// SPDY through the apiserver.
//
// Why not SPDY: a web UI (Grafana) opens dozens of parallel HTTP
// connections per page render — assets, websocket, one per panel,
// polling. client-go's SPDY port-forward multiplexes all of them
// onto one apiserver connection. Each browser disconnect (page
// navigation, panel completion, tab close) tears down a SPDY stream
// mid-flight and surfaces as runtime.HandleError "Unhandled Error:
// broken pipe". That's not noise to silence — it's the wrong
// transport for the workload.
//
// Raw TCP through SSH gives each browser connection its own
// independent SSH channel, clean close lifecycle (io.EOF on read,
// drop the goroutine), no multiplexing, no error spam.

import (
	"context"
	"fmt"
	"io"
	"net"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/ssh"
)

// PortForward forwards localPort on the operator's machine to
// remotePort on a Pod backing svcName in ns. Resolves the Service to
// one of its Ready Pods via the kube API (single read), then opens a
// local TCP listener that proxies each accepted connection through
// sh.DialTCP to the pod's IP — same shape openTunnel uses for the
// apiserver.
//
// Blocks until ctx is cancelled (typically ctrl-C from the operator
// running `nvoi monitor`). Returns ctx.Err() on cancellation.
//
// sh is the SSH client to the primary master. The k3s pod network
// (flannel) is reachable from any node, so the master can DialTCP
// directly to pod IPs across the cluster.
func (c *Client) PortForward(ctx context.Context, sh *ssh.Client, ns, svcName string, localPort, remotePort int) error {
	podIP, err := c.firstReadyPodIPFor(ctx, ns, svcName)
	if err != nil {
		return fmt.Errorf("resolve pod IP for %s/%s: %w", ns, svcName, err)
	}
	remote := fmt.Sprintf("%s:%d", podIP, remotePort)

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		return fmt.Errorf("listen on localhost:%d: %w", localPort, err)
	}
	defer ln.Close()

	// Accept loop in a goroutine — io.Copy on each accepted connection
	// is independent of every other. Connection close (EOF or reset)
	// drops one goroutine; the listener keeps serving.
	go acceptLoop(ln, sh, remote)

	<-ctx.Done()
	return ctx.Err()
}

// acceptLoop accepts incoming connections on ln and proxies each one
// through sh.DialTCP(remote). Each accepted connection runs in its
// own goroutine. Lives until ln.Close() returns from outside (via
// the deferred close in PortForward when ctx ends).
//
// Close semantics: io.Copy returns naturally on either side closing
// (browser navigates → local read returns EOF → io.Copy returns →
// both Conns close). No "Unhandled Error" path; close IS normal.
func acceptLoop(ln net.Listener, sh *ssh.Client, remote string) {
	for {
		local, err := ln.Accept()
		if err != nil {
			return // listener closed (ctx ended) — no need to spam
		}
		go proxyOne(local, sh, remote)
	}
}

// proxyOne dials the remote through SSH and shuttles bytes in both
// directions. Closes both sides on completion of either io.Copy.
func proxyOne(local net.Conn, sh *ssh.Client, remote string) {
	defer local.Close()
	r, err := sh.DialTCP(remote)
	if err != nil {
		return // SSH dial failure — drop silently, browser sees connection refused on next try
	}
	defer r.Close()

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(r, local)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(local, r)
		done <- struct{}{}
	}()
	// First direction to return EOF (or any error) ends the pair.
	<-done
}

// firstReadyPodIPFor returns the Pod IP of one Ready Pod backing
// svcName. Reads svc.Spec.Selector, lists matching pods, picks the
// first that's PodReady=True and has a non-empty PodIP.
//
// PodIP is what the master's SSH session resolves via k3s flannel —
// reachable across nodes without any additional setup.
func (c *Client) firstReadyPodIPFor(ctx context.Context, ns, svcName string) (string, error) {
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
		if isPodReady(p) && p.Status.PodIP != "" {
			return p.Status.PodIP, nil
		}
	}
	return "", fmt.Errorf("no Ready pod with IP for service %s/%s", ns, svcName)
}

func isPodReady(p corev1.Pod) bool {
	for _, cond := range p.Status.Conditions {
		if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
