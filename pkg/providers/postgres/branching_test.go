package postgres_test

import (
	"context"
	"io"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeobj "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	testingk8s "k8s.io/client-go/testing"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/providers/postgres"
)

// fakeShell implements ssh.Shell — captures every command for
// assertions and returns canned outputs by substring match. Reused
// across branch / snapshot tests where the kubectl-via-ssh path is
// what we're locking down.
type fakeShell struct {
	calls   []string
	respond func(cmd string) ([]byte, error)
}

func (s *fakeShell) Run(_ context.Context, cmd string) ([]byte, error) {
	s.calls = append(s.calls, cmd)
	if s.respond != nil {
		return s.respond(cmd)
	}
	return nil, nil
}

func (s *fakeShell) RunStream(_ context.Context, cmd string, _, _ io.Writer) error {
	s.calls = append(s.calls, cmd)
	return nil
}

func (s *fakeShell) Addr() string { return "fake" }

func TestSnapshot_DerivesNameAndAppliesYAML(t *testing.T) {
	sh := &fakeShell{}
	ref, err := postgres.Snapshot(context.Background(), nil, "myapp", "prod", "app", "pre-deploy")
	if err == nil || !strings.Contains(err.Error(), "master ssh required") {
		t.Fatalf("nil ssh: want master ssh error, got ref=%v err=%v", ref, err)
	}

	ref, err = postgres.Snapshot(context.Background(), sh, "myapp", "prod", "app", "pre-deploy")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	want := "nvoi-myapp-prod-db-app-snap-pre-deploy"
	if ref.Name != want {
		t.Errorf("snapshot name = %q, want %q", ref.Name, want)
	}
	if ref.Kind != "zfs" {
		t.Errorf("snapshot kind = %q, want zfs", ref.Kind)
	}

	// Two kubectl apply commands expected — SnapshotClass and
	// VolumeSnapshot, in that order.
	if len(sh.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(sh.calls))
	}
	if !strings.Contains(sh.calls[0], "VolumeSnapshotClass") {
		t.Errorf("first call should apply SnapshotClass, got %q", sh.calls[0])
	}
	if !strings.Contains(sh.calls[1], "VolumeSnapshot\n") {
		t.Errorf("second call should apply VolumeSnapshot, got %q", sh.calls[1])
	}
	if !strings.Contains(sh.calls[1], want) {
		t.Errorf("VolumeSnapshot manifest doesn't reference %q", want)
	}
}

func TestSnapshot_RejectsBadLabel(t *testing.T) {
	sh := &fakeShell{}
	cases := []string{"Bad-Label", "with space", "with.dot", "trailing-", "-leading"}
	for _, label := range cases {
		_, err := postgres.Snapshot(context.Background(), sh, "myapp", "prod", "app", label)
		if err == nil {
			t.Errorf("label %q accepted but should be rejected", label)
		}
	}
}

func TestListSnapshots_ParsesKubectlOutput(t *testing.T) {
	sh := &fakeShell{
		respond: func(cmd string) ([]byte, error) {
			return []byte("volumesnapshot/nvoi-myapp-prod-db-app-snap-pre-deploy\nvolumesnapshot/nvoi-myapp-prod-db-app-snap-post-migrate\n"), nil
		},
	}
	snaps, err := postgres.ListSnapshots(context.Background(), sh, "myapp", "prod", "app")
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %d, want 2", len(snaps))
	}
	if snaps[0].Name != "nvoi-myapp-prod-db-app-snap-pre-deploy" {
		t.Errorf("[0].Name = %q", snaps[0].Name)
	}
}

func TestDeleteSnapshot_ValidatesName(t *testing.T) {
	sh := &fakeShell{}
	if err := postgres.DeleteSnapshot(context.Background(), sh, "ns", "Bad-Name"); err == nil {
		t.Errorf("uppercase name should be rejected")
	}
	if err := postgres.DeleteSnapshot(context.Background(), sh, "ns", "valid-name"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
}

func TestBranch_WaitsForStatefulSetReady(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "statefulsets", func(action testingk8s.Action) (bool, runtimeobj.Object, error) {
		create := action.(testingk8s.CreateAction)
		ss := create.GetObject().(*appsv1.StatefulSet).DeepCopy()
		ss.Generation = 1
		if ss.Spec.Replicas == nil {
			replicas := int32(1)
			ss.Spec.Replicas = &replicas
		}
		ss.Status.ReadyReplicas = *ss.Spec.Replicas
		ss.Status.ObservedGeneration = ss.Generation
		if err := cs.Tracker().Add(ss); err != nil {
			return true, nil, err
		}
		return true, ss, nil
	})

	kc := kube.NewForTest(cs)
	sh := &fakeShell{}
	ref, err := postgres.Branch(context.Background(), kc, sh, postgres.BranchSource{
		App:        "myapp",
		Env:        "prod",
		DBName:     "app",
		Size:       20,
		Version:    "17",
		ServerRole: "db-worker",
	}, "pr-1")
	if err != nil {
		t.Fatalf("Branch: %v", err)
	}
	if ref.Endpoint != "nvoi-myapp-prod-db-app-br-pr-1.default.svc.cluster.local:5432" {
		t.Errorf("endpoint = %q", ref.Endpoint)
	}

	ss, err := cs.AppsV1().StatefulSets("default").Get(context.Background(), "nvoi-myapp-prod-db-app-br-pr-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get branch statefulset: %v", err)
	}
	if ss.Status.ReadyReplicas != 1 || ss.Status.ObservedGeneration != ss.Generation {
		t.Fatalf("branch statefulset was not made ready for the waiter: %+v", ss.Status)
	}
	if len(sh.calls) != 2 {
		t.Fatalf("snapshot apply calls = %d, want 2", len(sh.calls))
	}
}
