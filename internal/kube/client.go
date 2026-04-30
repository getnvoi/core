// Package kube is the typed Kubernetes client over an SSH-tunneled
// apiserver connection. Built once after k3s install completes; carried
// as a local in deploy.go's lifecycle (never on Runtime — same rule as
// runner.Runner).
//
// Why over SSH: the kube apiserver listens on the master's private IP
// (firewalled off from public). Without a tunnel we'd have to publish
// 6443 to the internet OR run kubectl exclusively from inside the
// cluster. Tunneling gets us a typed in-Go client without exposing the
// apiserver.
//
// Why typed and not shell kubectl: Apply / streaming watch / typed
// errors / FieldManager-aware updates / programmatic manifest
// construction all matter the moment we move past install diagnostics
// and into workload deployment. Shell kubectl stays for ad-hoc cluster
// queries (install/kubectl.go); this package is for in-Go programmatic
// operations.
package kube

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/getnvoi/core/internal/ssh"
)

// FieldManager identifies nvoi in the apiserver's owner-tracking.
// Updates from anyone with a different FieldManager are preserved
// rather than overwritten — operator can edit non-managed fields with
// kubectl without nvoi "fighting" them.
const FieldManager = "nvoi"

// Client wraps a typed Kubernetes clientset connected via SSH-forwarded
// listener. Caller must Close() to release the listener and SSH channel
// when done.
type Client struct {
	CS      kubernetes.Interface // typed clientset
	cfg     *rest.Config
	apiHost string // original apiserver host (kubeconfig.cluster.server) — used for TLS ServerName
	cleanup func() // closes the local listener + accept loop
}

// New builds the client by:
//
//  1. SFTP-fetching the deploy-user kubeconfig from the master,
//  2. Opening an SSH-tunneled local TCP listener on a free localhost port,
//  3. Rewriting kubeconfig.server to point at the local tunnel,
//  4. Pinning TLSClientConfig.ServerName to the original apiserver host
//     so cert validation matches the SANs we set on --tls-san,
//  5. Building a rest.Config and a typed clientset.
//
// Caller must Close() when done.
func New(ctx context.Context, sh *ssh.Client) (*Client, error) {
	raw, err := fetchKubeconfig(ctx, sh)
	if err != nil {
		return nil, fmt.Errorf("fetch kubeconfig: %w", err)
	}

	apiHost, err := apiserverHost(raw)
	if err != nil {
		return nil, err
	}

	tunnel, cleanup, err := openTunnel(sh, apiHost)
	if err != nil {
		return nil, fmt.Errorf("open kube tunnel to %s: %w", apiHost, err)
	}

	cfg, err := buildRESTConfig(raw, tunnel, apiHost)
	if err != nil {
		cleanup()
		return nil, err
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("kubernetes clientset: %w", err)
	}

	return &Client{CS: cs, cfg: cfg, apiHost: apiHost, cleanup: cleanup}, nil
}

// Close releases the SSH tunnel listener and accept loop. Idempotent.
func (c *Client) Close() error {
	if c.cleanup != nil {
		c.cleanup()
		c.cleanup = nil
	}
	return nil
}

// fetchKubeconfig reads /home/deploy/.kube/config from the master.
// That copy was rewritten by install/primary.go::setupKubeconfig to
// use the master's private IP instead of 127.0.0.1, so the URL the
// kube tunnel forwards to is reachable from any node-network endpoint.
func fetchKubeconfig(ctx context.Context, sh *ssh.Client) ([]byte, error) {
	out, err := sh.Run(ctx, "cat /home/deploy/.kube/config")
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, fmt.Errorf("kubeconfig empty — primary install may have failed")
	}
	return out, nil
}

// apiserverHost extracts host:port from the kubeconfig's current
// cluster's Server URL. Used for both the tunnel target AND the TLS
// ServerName pin.
func apiserverHost(raw []byte) (string, error) {
	cfg, err := clientcmd.Load(raw)
	if err != nil {
		return "", fmt.Errorf("parse kubeconfig: %w", err)
	}
	ctxName := cfg.CurrentContext
	if ctxName == "" {
		// Fallback: pick the only context if there's exactly one.
		if len(cfg.Contexts) == 1 {
			for k := range cfg.Contexts {
				ctxName = k
			}
		}
	}
	if ctxName == "" {
		return "", fmt.Errorf("kubeconfig has no current-context")
	}
	kctx := cfg.Contexts[ctxName]
	if kctx == nil {
		return "", fmt.Errorf("context %q missing", ctxName)
	}
	cluster := cfg.Clusters[kctx.Cluster]
	if cluster == nil {
		return "", fmt.Errorf("cluster %q missing in kubeconfig", kctx.Cluster)
	}
	u, err := url.Parse(cluster.Server)
	if err != nil {
		return "", fmt.Errorf("parse cluster server URL: %w", err)
	}
	host := u.Host
	if host == "" {
		return "", fmt.Errorf("cluster server URL %q missing host", cluster.Server)
	}
	if !strings.Contains(host, ":") {
		host = host + ":6443"
	}
	return host, nil
}

// openTunnel opens a localhost listener that forwards each accepted
// connection through ssh.DialTCP to remoteAddr. Lives until cleanup()
// is called.
//
// DialTCP errors are swallowed — client-go's transport retries on
// connection failures, and we don't want every transient SSH reconnect
// to surface as a CLI error. If something is genuinely broken, the
// next API call surfaces a typed "connection refused".
func openTunnel(sh *ssh.Client, remoteAddr string) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("listen: %w", err)
	}
	addr := ln.Addr().String()

	go func() {
		for {
			local, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go func() {
				defer local.Close()
				remote, err := sh.DialTCP(remoteAddr)
				if err != nil {
					return
				}
				defer remote.Close()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(remote, local); close(done) }()
				_, _ = io.Copy(local, remote)
				<-done
			}()
		}
	}()

	return addr, func() { _ = ln.Close() }, nil
}

// buildRESTConfig produces a *rest.Config that:
//   - dials the local tunnel address,
//   - validates TLS using the CA embedded in the fetched kubeconfig
//     with ServerName pinned to the apiserver's real host so the
//     cert SANs (--tls-san priv + pub + LB) match.
//
// Going through clientcmd's deferred-loading path would silently pick
// up the operator's local ~/.kube/config and validate against the
// wrong CA. We always parse the bytes we just fetched.
func buildRESTConfig(raw []byte, tunnelAddr, apiHost string) (*rest.Config, error) {
	apiCfg, err := clientcmd.Load(raw)
	if err != nil {
		return nil, fmt.Errorf("parse kubeconfig: %w", err)
	}
	overrides := &clientcmd.ConfigOverrides{
		ClusterInfo: clientcmdapi.Cluster{
			Server:        "https://" + tunnelAddr,
			TLSServerName: hostOnly(apiHost),
		},
	}
	cfg, err := clientcmd.NewDefaultClientConfig(*apiCfg, overrides).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build rest config: %w", err)
	}
	cfg.Timeout = 30 * time.Second
	return cfg, nil
}

// hostOnly strips :port from host:port. Used to derive the TLS
// ServerName from the apiserver address (cert SANs are hostnames/IPs
// without ports).
func hostOnly(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		return addr[:i]
	}
	return addr
}
