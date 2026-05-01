package detach_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/detach"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/testutil/sshfake"
)

// silentLog discards everything; tests assert on sh.Calls instead.
func silentLog() log.Log { return log.NewWith(false, io.Discard) }

func TestNodes_WorkerDrainsThenDeletes(t *testing.T) {
	sh := &sshfake.Shell{
		Address: "10.0.0.1:22",
		Matchers: []sshfake.Match{
			{Contains: "kubectl drain", Resp: sshfake.Response{Stdout: []byte("node/x drained\n")}},
			{Contains: "kubectl delete node", Resp: sshfake.Response{Stdout: []byte("node \"x\" deleted\n")}},
		},
	}

	detach.Nodes(context.Background(), sh, []detach.Node{
		{Hostname: "worker-2", Role: "worker"},
	}, silentLog())

	// Worker: drain + delete-node only. NO etcd ops.
	if len(sh.Calls) != 2 {
		t.Fatalf("expected 2 calls (drain + delete-node), got %d:\n%s", len(sh.Calls), strings.Join(sh.Calls, "\n"))
	}
	if !strings.Contains(sh.Calls[0], "drain worker-2") {
		t.Errorf("call[0] missing 'drain worker-2': %q", sh.Calls[0])
	}
	if !strings.Contains(sh.Calls[1], "delete node worker-2") {
		t.Errorf("call[1] missing 'delete node worker-2': %q", sh.Calls[1])
	}
	for _, c := range sh.Calls {
		if strings.Contains(c, "etcdctl") || strings.Contains(c, "apt-get install") {
			t.Errorf("workers must NOT touch etcd: %q", c)
		}
	}
}

func TestNodes_MasterRemovesEtcdBeforeDeletingNode(t *testing.T) {
	sh := &sshfake.Shell{
		Address: "10.0.0.1:22",
		Matchers: []sshfake.Match{
			{Contains: "kubectl drain", Resp: sshfake.Response{}},
			{Contains: "command -v etcdctl", Resp: sshfake.Response{}}, // already installed (no apt-get)
			{Contains: "member list", Resp: sshfake.Response{
				Stdout: []byte("a1b2c3d4, started, master-3-cad40679, https://10.0.1.4:2380, https://10.0.1.4:2379, false\n"),
			}},
			{Contains: "member remove", Resp: sshfake.Response{Stdout: []byte("Member a1b2c3d4 removed\n")}},
			{Contains: "kubectl delete node", Resp: sshfake.Response{Stdout: []byte("node \"master-3\" deleted\n")}},
		},
	}

	detach.Nodes(context.Background(), sh, []detach.Node{
		{Hostname: "master-3", Role: "master"},
	}, silentLog())

	idx := func(needle string) int {
		for i, c := range sh.Calls {
			if strings.Contains(c, needle) {
				return i
			}
		}
		return -1
	}
	drainAt := idx("drain master-3")
	memberRemoveAt := idx("member remove a1b2c3d4")
	deleteNodeAt := idx("delete node master-3")

	if drainAt < 0 {
		t.Errorf("drain master-3 never called; calls:\n%s", strings.Join(sh.Calls, "\n"))
	}
	if memberRemoveAt < 0 {
		t.Errorf("etcdctl member remove never called for master; calls:\n%s", strings.Join(sh.Calls, "\n"))
	}
	if deleteNodeAt < 0 {
		t.Errorf("kubectl delete node never called; calls:\n%s", strings.Join(sh.Calls, "\n"))
	}
	// Critical ordering: etcd member remove must happen BEFORE we
	// delete the node from the apiserver. Otherwise etcd retries
	// against a node the apiserver already forgot about.
	if memberRemoveAt >= 0 && deleteNodeAt >= 0 && memberRemoveAt > deleteNodeAt {
		t.Errorf("etcd member remove (idx=%d) must come BEFORE delete-node (idx=%d)", memberRemoveAt, deleteNodeAt)
	}
}

func TestNodes_EtcdctlInstalledOnDemand(t *testing.T) {
	// command -v etcdctl returns non-zero (binary missing) → ensureEtcdctl
	// must apt-install it before running etcdctl member list.
	sh := &sshfake.Shell{
		Matchers: []sshfake.Match{
			{Contains: "kubectl drain", Resp: sshfake.Response{}},
			{Contains: "command -v etcdctl", Resp: sshfake.Response{Err: errAbsent("etcdctl")}},
			{Contains: "apt-get install", Resp: sshfake.Response{}},
			{Contains: "member list", Resp: sshfake.Response{Stdout: []byte("aaa, started, m-x, ...\n")}},
			{Contains: "member remove", Resp: sshfake.Response{}},
			{Contains: "kubectl delete node", Resp: sshfake.Response{}},
		},
	}

	detach.Nodes(context.Background(), sh, []detach.Node{
		{Hostname: "m", Role: "master"},
	}, silentLog())

	var sawApt bool
	for _, c := range sh.Calls {
		if strings.Contains(c, "apt-get install") && strings.Contains(c, "etcd-client") {
			sawApt = true
			break
		}
	}
	if !sawApt {
		t.Errorf("expected apt-get install etcd-client when binary missing; calls:\n%s", strings.Join(sh.Calls, "\n"))
	}
}

func TestNodes_EtcdMemberAlreadyGoneIsNoOp(t *testing.T) {
	// k3s already auto-cleaned the etcd member: member list does not
	// include our hostname → removeEtcdMember returns nil silently
	// (no remove call), detach proceeds to delete-node.
	sh := &sshfake.Shell{
		Matchers: []sshfake.Match{
			{Contains: "kubectl drain", Resp: sshfake.Response{}},
			{Contains: "command -v etcdctl", Resp: sshfake.Response{}},
			{Contains: "member list", Resp: sshfake.Response{
				// only OTHER masters present; "m-gone" not in the list
				Stdout: []byte("aaa, started, m-1-pfx, ...\nbbb, started, m-2-pfx, ...\n"),
			}},
			{Contains: "kubectl delete node", Resp: sshfake.Response{}},
		},
	}

	detach.Nodes(context.Background(), sh, []detach.Node{
		{Hostname: "m-gone", Role: "master"},
	}, silentLog())

	for _, c := range sh.Calls {
		if strings.Contains(c, "member remove") {
			t.Errorf("member remove should NOT be called when member is already gone: %q", c)
		}
	}
}

// errAbsent is a tiny sentinel for "command not found" — anything
// non-nil will do; sshfake just propagates it.
type errAbsentErr struct{ what string }

func (e errAbsentErr) Error() string { return e.what + ": not installed" }
func errAbsent(what string) error    { return errAbsentErr{what} }
