package postgres_test

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/providers/postgres"
)

// canonical request shape — every test that doesn't exercise the
// optional fields (Bucket, NodeSSH, MasterSSH) starts from here.
func baseReq() providers.DatabaseRequest {
	return providers.DatabaseRequest{
		App:                   "myapp",
		Env:                   "prod",
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		Namespace:             "nvoi-myapp-prod",
		PodName:               "nvoi-myapp-prod-db-app-0",
		PVCName:               "nvoi-myapp-prod-db-app-data",
		BackupName:            "nvoi-myapp-prod-db-app-backup",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
		Spec: providers.DatabaseSpec{
			Engine:   "postgres",
			Version:  "17",
			Server:   "db-worker",
			Size:     20,
			User:     "app_user",
			Password: "app_pw",
			Database: "app_db",
		},
	}
}

func TestImageFor_DefaultsAndOverride(t *testing.T) {
	if got := postgres.ImageFor(""); got != "postgres:17-alpine" {
		t.Errorf("ImageFor(\"\") = %q, want postgres:17-alpine", got)
	}
	if got := postgres.ImageFor("16"); got != "postgres:16-alpine" {
		t.Errorf("ImageFor(16) = %q", got)
	}
}

func TestReconcile_EmitsCanonicalWorkloads(t *testing.T) {
	req := baseReq()
	p := &postgres.Provider{}
	plan, err := p.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(plan.Workloads) != 4 {
		t.Fatalf("workloads = %d, want 4 (SC + Service + PVC + StatefulSet)", len(plan.Workloads))
	}

	// Index by Kind for assertions.
	got := map[string]any{}
	for _, w := range plan.Workloads {
		switch w := w.(type) {
		case *storagev1.StorageClass:
			got["StorageClass"] = w
		case *corev1.Service:
			got["Service"] = w
		case *corev1.PersistentVolumeClaim:
			got["PVC"] = w
		case *appsv1.StatefulSet:
			got["StatefulSet"] = w
		}
	}
	if _, ok := got["StorageClass"]; !ok {
		t.Error("missing StorageClass workload")
	}
	if svc, ok := got["Service"].(*corev1.Service); !ok || svc.Name != req.FullName {
		t.Errorf("Service name = %v, want %s", svc, req.FullName)
	}
	if pvc, ok := got["PVC"].(*corev1.PersistentVolumeClaim); !ok || pvc.Name != req.PVCName {
		t.Errorf("PVC name = %v", pvc)
	}
	ss, ok := got["StatefulSet"].(*appsv1.StatefulSet)
	if !ok {
		t.Fatal("missing StatefulSet workload")
	}
	if ss.Spec.Template.Spec.NodeSelector["nvoi-role"] != "db-worker" {
		t.Errorf("nodeSelector = %v, want nvoi-role=db-worker", ss.Spec.Template.Spec.NodeSelector)
	}
	if got := ss.Spec.Template.Spec.Containers[0].Image; got != "postgres:17-alpine" {
		t.Errorf("postgres image = %q", got)
	}
	// POSTGRES_USER/PASSWORD/DB env vars are populated from req.Spec.
	envs := map[string]string{}
	for _, e := range ss.Spec.Template.Spec.Containers[0].Env {
		envs[e.Name] = e.Value
	}
	for _, k := range []string{"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"} {
		if envs[k] == "" {
			t.Errorf("env %s not set", k)
		}
	}
}

func TestReconcile_BackupAppendsCronJob(t *testing.T) {
	req := baseReq()
	req.Spec.Backup = &providers.DatabaseBackupSpec{Schedule: "0 3 * * *", Retention: 14}
	req.BackupCredsSecretName = "nvoi-myapp-prod-db-app-backup-creds"
	p := &postgres.Provider{}
	plan, err := p.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(plan.Workloads) != 5 {
		t.Fatalf("workloads = %d, want 5 (4 + backup CronJob)", len(plan.Workloads))
	}
}

func TestEnsureCredentials_WritesCanonicalSecret(t *testing.T) {
	cs := fake.NewSimpleClientset()
	kc := kube.NewForTest(cs)
	req := baseReq()

	p := &postgres.Provider{}
	creds, err := p.EnsureCredentials(context.Background(), kc, req)
	if err != nil {
		t.Fatalf("EnsureCredentials: %v", err)
	}
	if !strings.Contains(creds.URL, "postgres://app_user:app_pw@nvoi-myapp-prod-db-app:5432/app_db") {
		t.Errorf("URL = %q", creds.URL)
	}

	got, err := cs.CoreV1().Secrets(req.Namespace).Get(context.Background(), req.CredentialsSecretName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	for _, key := range []string{"url", "host", "port", "user", "password", "database", "sslmode"} {
		if _, ok := got.Data[key]; !ok {
			t.Errorf("credentials Secret missing canonical key %q", key)
		}
	}
	if string(got.Data["host"]) != "nvoi-myapp-prod-db-app" {
		t.Errorf("host = %q", string(got.Data["host"]))
	}
	if got.Labels[kube.LabelOwner] != kube.OwnerDatabases {
		t.Errorf("owner label = %q, want %q", got.Labels[kube.LabelOwner], kube.OwnerDatabases)
	}
}

func TestProvider_Registered(t *testing.T) {
	// register.go's init() ran via the blank import in this _test.go's
	// file-level imports — verify postgres is in the registry.
	if !providers.IsRegisteredDatabase("postgres") {
		t.Fatalf("postgres engine not registered (expected via init())")
	}
}

func TestSnapshot_RequiresMasterSSH(t *testing.T) {
	p := &postgres.Provider{}
	req := baseReq()
	_, err := p.Snapshot(context.Background(), req, "pre-deploy")
	if err == nil || !strings.Contains(err.Error(), "master ssh required") {
		t.Errorf("want master-ssh error, got %v", err)
	}
}

func TestBranch_RequiresKubeAndMasterSSH(t *testing.T) {
	p := &postgres.Provider{}
	req := baseReq()
	_, err := p.Branch(context.Background(), req, "pr-1")
	if err == nil || !strings.Contains(err.Error(), "kube client + master ssh required") {
		t.Errorf("want kc+ssh error, got %v", err)
	}
}
