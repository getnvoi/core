// Package ssh is the SSH client every post-apply install stage uses.
// Ported from nvoi/pkg/infra/ssh.go, scope-trimmed: Dial + Run +
// RunStream + Close. SFTP, port forwarding, and kube tunneling come
// when we wire workload deployment.
//
// Lifetime: opened by deploy.go after terraform.Apply, deferred-closed
// when the verb returns. Per-stage handle, not stored on Runtime.
package ssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"
)

// Shell is the narrow interface install + detach packages depend on.
// Lets tests substitute a fake without holding a real SSH connection.
// *Client implements it (Run + RunStream below). Kept tight: anything
// needing TCP forwarding (kube tunnel) keeps using *Client directly.
type Shell interface {
	Run(ctx context.Context, cmd string) ([]byte, error)
	RunStream(ctx context.Context, cmd string, stdout, stderr io.Writer) error
	Addr() string // host:port — used in log/error messages
}

// Client wraps a persistent SSH connection.
type Client struct {
	conn *gossh.Client
	addr string
	user string
}

// Dial opens an SSH connection. user is typically "deploy" (matches
// what cloud-init created). privateKey is the PEM bytes of the operator's
// private key (read at the cmd/ boundary).
//
// Host key callback is InsecureIgnoreHostKey for now — fresh boxes
// every cold start, no persistent known_hosts. Hardening lands when
// we promote this beyond POC.
func Dial(ctx context.Context, addr, user string, privateKey []byte) (*Client, error) {
	signer, err := gossh.ParsePrivateKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	cfg := &gossh.ClientConfig{
		User:            user,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	type result struct {
		conn *gossh.Client
		err  error
	}
	done := make(chan result, 1)
	go func() {
		c, err := gossh.Dial("tcp", addr, cfg)
		done <- result{c, err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-done:
		if r.err != nil {
			if isAuthFailure(r.err) {
				return nil, fmt.Errorf("ssh dial %s: authentication failed: %w", addr, r.err)
			}
			return nil, fmt.Errorf("ssh dial %s: %w", addr, r.err)
		}
		return &Client{conn: r.conn, addr: addr, user: user}, nil
	}
}

// Run executes cmd and returns its combined output. Honors ctx
// cancellation by signalling the remote process.
func (c *Client) Run(ctx context.Context, cmd string) ([]byte, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	type result struct {
		out []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := sess.CombinedOutput(cmd)
		done <- result{out, err}
	}()

	select {
	case <-ctx.Done():
		_ = sess.Signal(gossh.SIGKILL)
		_ = sess.Close()
		<-done
		return nil, ctx.Err()
	case r := <-done:
		if r.err != nil {
			detail := strings.TrimSpace(string(r.out))
			if detail != "" {
				return r.out, fmt.Errorf("ssh run %q on %s: %s: %w", cmd, c.addr, detail, r.err)
			}
			return r.out, fmt.Errorf("ssh run %q on %s: %w", cmd, c.addr, r.err)
		}
		return r.out, nil
	}
}

// RunStream executes cmd, streaming stdout/stderr to the given writers.
// Used for loud commands like the k3s installer. Honors ctx cancellation.
func (c *Client) RunStream(ctx context.Context, cmd string, stdout, stderr io.Writer) error {
	sess, err := c.conn.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()

	sess.Stdout = stdout
	sess.Stderr = stderr

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case <-ctx.Done():
		_ = sess.Signal(gossh.SIGKILL)
		_ = sess.Close()
		<-done
		return ctx.Err()
	case err := <-done:
		if err != nil {
			return fmt.Errorf("ssh run %q on %s: %w", cmd, c.addr, err)
		}
		return nil
	}
}

// DialTCP opens a TCP connection to remoteAddr through the SSH
// channel — used by internal/kube to forward apiserver traffic.
// remoteAddr is host:port relative to the SSH'd box (e.g. the
// master's private IP:6443, or 127.0.0.1:2019 for Caddy admin).
func (c *Client) DialTCP(remoteAddr string) (net.Conn, error) {
	conn, err := c.conn.Dial("tcp", remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("ssh dial-tcp %s via %s: %w", remoteAddr, c.addr, err)
	}
	return conn, nil
}

// Close shuts down the SSH connection.
func (c *Client) Close() error { return c.conn.Close() }

// Addr returns the host:port the client is connected to.
func (c *Client) Addr() string { return c.addr }

// User returns the SSH login user.
func (c *Client) User() string { return c.user }

func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unable to authenticate") ||
		strings.Contains(msg, "no supported methods remain")
}
