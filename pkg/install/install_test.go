package install_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/ssh"
	"github.com/getnvoi/core/pkg/testutil/sshfake"
)

func silentLog() log.Log { return log.NewWith(false, io.Discard) }

// ── DiscoverToken ─────────────────────────────────────────────────

func TestDiscoverToken_NoneRunning_ReturnsFalse(t *testing.T) {
	sh := &sshfake.Shell{
		Matchers: []sshfake.Match{
			// systemctl is-active --quiet k3s returns non-zero everywhere
			{Contains: "is-active --quiet k3s", Resp: sshfake.Response{Err: errors.New("inactive")}},
		},
	}
	masters := map[string]ssh.Shell{"m1": sh, "m2": sh}

	tok, found, err := install.DiscoverToken(context.Background(), masters)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if found || tok != "" {
		t.Errorf("expected no token; got %q found=%v", tok, found)
	}
}

func TestDiscoverToken_FirstHealthyMasterWins(t *testing.T) {
	dead := &sshfake.Shell{
		Matchers: []sshfake.Match{
			{Contains: "is-active --quiet k3s", Resp: sshfake.Response{Err: errors.New("inactive")}},
		},
	}
	live := &sshfake.Shell{
		Matchers: []sshfake.Match{
			{Contains: "is-active --quiet k3s", Resp: sshfake.Response{}}, // active
			{Contains: "cat /var/lib/rancher/k3s/server/node-token", Resp: sshfake.Response{
				Stdout: []byte("K10secrettokenvalue\n"),
			}},
		},
	}

	tok, found, err := install.DiscoverToken(context.Background(), map[string]ssh.Shell{
		"dead": dead,
		"live": live,
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !found {
		t.Errorf("expected found=true")
	}
	if tok != "K10secrettokenvalue" {
		t.Errorf("got token %q want %q", tok, "K10secrettokenvalue")
	}
}

// ── InstallPrimaryMaster command shape ────────────────────────────

// Matches every k3s install: server already-Ready check fails (not
// installed) → discover iface → curl|sh runs → setup kubeconfig →
// WaitNodeReady polls until kubectl reports Ready.
func k3sInstallShell(extraMatchers ...sshfake.Match) *sshfake.Shell {
	base := []sshfake.Match{
		// localNodeReady probe: k3s binary missing → not installed yet
		{Contains: "command -v k3s", Resp: sshfake.Response{Err: errors.New("absent")}},
		// discoverPrivateInterface: ip -o -4 addr show … → return iface name
		{Contains: "ip -o -4 addr show", Resp: sshfake.Response{Stdout: []byte("enp7s0\n")}},
		// curl|sh installer: pretend it succeeded
		{Contains: "curl -sfL https://get.k3s.io", Resp: sshfake.Response{}},
		// setupKubeconfig: just accept the chained shell
		{Contains: "mkdir -p /home/deploy/.kube", Resp: sshfake.Response{}},
		// WaitNodeReady: kubectl get nodes returning Ready immediately
		{Contains: "kubectl get nodes", Resp: sshfake.Response{
			Stdout: []byte("True,nvoi-hello-dev-master\n"),
		}},
	}
	return &sshfake.Shell{Matchers: append(extraMatchers, base...)}
}

func TestInstallPrimaryMaster_CommandHasClusterInitAndTLSSANs(t *testing.T) {
	sh := k3sInstallShell()
	node := install.Node{
		Name:     "master",
		Hostname: "nvoi-hello-dev-master",
		IPv4:     "1.2.3.4",
		Private:  "10.0.1.1",
		Shell:    sh,
		Log:      silentLog(),
	}
	extraSANs := []string{"10.0.1.99", "5.6.7.8"} // LB priv + pub

	if err := install.InstallPrimaryMaster(context.Background(), node, extraSANs); err != nil {
		t.Fatalf("InstallPrimaryMaster: %v", err)
	}

	var installer string
	for _, c := range sh.Calls {
		if strings.Contains(c, "curl -sfL https://get.k3s.io") {
			installer = c
			break
		}
	}
	if installer == "" {
		t.Fatalf("k3s installer never called; calls:\n%s", strings.Join(sh.Calls, "\n"))
	}

	// Required flags
	for _, want := range []string{
		"--cluster-init",
		"--node-ip 10.0.1.1",
		"--advertise-address 10.0.1.1",
		"--cluster-cidr 10.42.0.0/16",
		"--service-cidr 10.43.0.0/16",
		"--flannel-iface enp7s0",
		"--tls-san 10.0.1.1",  // self.Private
		"--tls-san 1.2.3.4",   // self.IPv4
		"--tls-san 10.0.1.99", // extra LB priv
		"--tls-san 5.6.7.8",   // extra LB pub
	} {
		if !strings.Contains(installer, want) {
			t.Errorf("installer cmd missing %q", want)
		}
	}
}

// ── JoinSecondaryMaster ───────────────────────────────────────────

func TestJoinSecondaryMaster_UsesServerFlagWithToken(t *testing.T) {
	sh := k3sInstallShell()
	self := install.Node{Name: "m2", Hostname: "nvoi-h-master-2", IPv4: "5.5.5.5", Private: "10.0.1.2", Shell: sh, Log: silentLog()}
	primary := install.Node{Name: "m1", Hostname: "nvoi-h-master-1", IPv4: "1.1.1.1", Private: "10.0.1.1"}

	if err := install.JoinSecondaryMaster(context.Background(), install.SecondaryJoinSpec{
		Self: self, Primary: primary, Token: "K10TOKEN",
	}); err != nil {
		t.Fatalf("JoinSecondaryMaster: %v", err)
	}

	var installer string
	for _, c := range sh.Calls {
		if strings.Contains(c, "curl -sfL https://get.k3s.io") {
			installer = c
			break
		}
	}
	if installer == "" {
		t.Fatalf("k3s installer never called")
	}
	for _, want := range []string{
		"--server https://10.0.1.1:6443", // primary's private
		"--token K10TOKEN",
		"--node-ip 10.0.1.2", // self.Private
	} {
		if !strings.Contains(installer, want) {
			t.Errorf("secondary join cmd missing %q", want)
		}
	}
	// must NOT have --cluster-init (that's primary-only)
	if strings.Contains(installer, "--cluster-init") {
		t.Errorf("secondary join must NOT have --cluster-init: %q", installer)
	}
}

// ── JoinWorker ────────────────────────────────────────────────────

func TestJoinWorker_PassesK3SURLAndToken(t *testing.T) {
	sh := &sshfake.Shell{
		Matchers: []sshfake.Match{
			// k3s-agent active probe: fails → worker is fresh
			{Contains: "is-active --quiet k3s-agent", Resp: sshfake.Response{Err: errors.New("inactive")}},
			{Contains: "ip -o -4 addr show", Resp: sshfake.Response{Stdout: []byte("ens10\n")}},
			{Contains: "curl -sfL https://get.k3s.io", Resp: sshfake.Response{}},
		},
	}
	self := install.Node{Name: "w1", Hostname: "nvoi-h-worker-1", IPv4: "9.9.9.9", Private: "10.0.1.10", Shell: sh, Log: silentLog()}
	target := "10.0.1.99" // LB private IP (HA case)

	if err := install.JoinWorker(context.Background(), install.WorkerJoinSpec{
		Self: self, Target: target, Token: "K10TOKEN",
	}); err != nil {
		t.Fatalf("JoinWorker: %v", err)
	}

	var installer string
	for _, c := range sh.Calls {
		if strings.Contains(c, "curl -sfL https://get.k3s.io") {
			installer = c
			break
		}
	}
	if installer == "" {
		t.Fatalf("worker installer never called; calls:\n%s", strings.Join(sh.Calls, "\n"))
	}
	for _, want := range []string{
		"K3S_URL=https://10.0.1.99:6443",
		"K3S_TOKEN=K10TOKEN",
		"agent", // INSTALL_K3S_EXEC starts with agent
		"--node-ip 10.0.1.10",
		"--flannel-iface ens10",
	} {
		if !strings.Contains(installer, want) {
			t.Errorf("worker join cmd missing %q\nfull: %s", want, installer)
		}
	}
}

func TestJoinWorker_AlreadyJoined_IsNoop(t *testing.T) {
	sh := &sshfake.Shell{
		Matchers: []sshfake.Match{
			// k3s-agent IS active → JoinWorker should return early
			{Contains: "is-active --quiet k3s-agent", Resp: sshfake.Response{}},
		},
	}
	self := install.Node{Hostname: "nvoi-h-worker-1", Private: "10.0.1.10", Shell: sh, Log: silentLog()}

	if err := install.JoinWorker(context.Background(), install.WorkerJoinSpec{
		Self: self, Target: "10.0.1.99", Token: "K10TOKEN",
	}); err != nil {
		t.Fatalf("JoinWorker: %v", err)
	}
	for _, c := range sh.Calls {
		if strings.Contains(c, "curl -sfL https://get.k3s.io") {
			t.Errorf("idempotent: should NOT re-run installer when k3s-agent is active: %q", c)
		}
	}
}
