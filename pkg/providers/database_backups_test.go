package providers

import (
	"strings"
	"testing"
)

// TestBuildBackupCronJob_DBCredsEnvBinding locks the canonical DB_*
// env-var mapping the cmd/db image's entrypoint depends on. If any
// of these break, scheduled backups would silently fail at 3am with
// "DB_URL not set" — the test cost here is one minute against weeks
// of paged on-call.
func TestBuildBackupCronJob_DBCredsEnvBinding(t *testing.T) {
	req := DatabaseRequest{
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		Namespace:             "nvoi-myapp-prod",
		BackupName:            "nvoi-myapp-prod-db-app-backup",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
		BackupCredsSecretName: "nvoi-myapp-prod-db-app-backup-creds",
		Spec: DatabaseSpec{
			Engine: "postgres",
			Backup: &DatabaseBackupSpec{Schedule: "0 3 * * *", Retention: 14},
		},
	}
	cj := BuildBackupCronJob(req)
	if cj.Spec.Schedule != "0 3 * * *" {
		t.Errorf("schedule = %q", cj.Spec.Schedule)
	}
	envs := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env

	// Expected: ENGINE, DATABASE_NAME, DATABASE_FULL_NAME, plus the
	// seven DB_* SecretKeyRef bindings.
	want := map[string]string{
		"ENGINE":             "postgres",
		"DATABASE_NAME":      "app",
		"DATABASE_FULL_NAME": "nvoi-myapp-prod-db-app",
	}
	got := map[string]string{}
	for _, e := range envs {
		if e.Value != "" {
			got[e.Name] = e.Value
		}
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env %s = %q, want %q", k, got[k], v)
		}
	}

	// SecretKeyRef bindings — must point at CredentialsSecretName
	// with the canonical lowercase keys.
	wantRefs := map[string]string{
		"DB_URL": "url", "DB_HOST": "host", "DB_PORT": "port",
		"DB_USER": "user", "DB_PASSWORD": "password",
		"DB_DATABASE": "database", "DB_SSLMODE": "sslmode",
	}
	gotRefs := map[string]string{}
	for _, e := range envs {
		if e.ValueFrom != nil && e.ValueFrom.SecretKeyRef != nil {
			if e.ValueFrom.SecretKeyRef.Name != req.CredentialsSecretName {
				t.Errorf("env %s references Secret %q, want %q", e.Name, e.ValueFrom.SecretKeyRef.Name, req.CredentialsSecretName)
			}
			gotRefs[e.Name] = e.ValueFrom.SecretKeyRef.Key
		}
	}
	for k, v := range wantRefs {
		if gotRefs[k] != v {
			t.Errorf("SecretKeyRef %s.key = %q, want %q", k, gotRefs[k], v)
		}
	}

	// envFrom must reference the backup-creds Secret (BUCKET_* + AWS_*).
	if got := cj.Spec.JobTemplate.Spec.Template.Spec.Containers[0].EnvFrom; len(got) != 1 || got[0].SecretRef.Name != req.BackupCredsSecretName {
		t.Errorf("envFrom = %v, want one entry pointing at %q", got, req.BackupCredsSecretName)
	}
}

// TestBuildRestoreJob_FlipsMode locks the only delta from BuildBackupCronJob:
// MODE=restore + BACKUP_KEY=<key>. Everything else (DB_* SecretKeyRef,
// EnvFrom shape, image ref) is identical — drift here would mean
// the backup pipeline works but restore silently fails.
func TestBuildRestoreJob_FlipsMode(t *testing.T) {
	req := DatabaseRequest{
		Name:                  "app",
		FullName:              "nvoi-myapp-prod-db-app",
		Namespace:             "nvoi-myapp-prod",
		CredentialsSecretName: "nvoi-myapp-prod-db-app-credentials",
		BackupCredsSecretName: "nvoi-myapp-prod-db-app-backup-creds",
		Spec:                  DatabaseSpec{Engine: "postgres"},
	}
	job := BuildRestoreJob(req, "20260101T030000Z.sql.gz")
	if !strings.HasPrefix(job.Name, "nvoi-myapp-prod-db-app-restore-") {
		t.Errorf("name = %q, want prefix nvoi-myapp-prod-db-app-restore-", job.Name)
	}

	envs := job.Spec.Template.Spec.Containers[0].Env
	got := map[string]string{}
	for _, e := range envs {
		if e.Value != "" {
			got[e.Name] = e.Value
		}
	}
	if got["MODE"] != "restore" {
		t.Errorf("MODE = %q, want restore", got["MODE"])
	}
	if got["BACKUP_KEY"] != "20260101T030000Z.sql.gz" {
		t.Errorf("BACKUP_KEY = %q", got["BACKUP_KEY"])
	}

	// Labels include the restore-of marker so the Job is traceable
	// back to the source DB in `kubectl get -L`.
	if job.Labels["nvoi/restore-of"] != "app" {
		t.Errorf("restore-of label = %q, want app", job.Labels["nvoi/restore-of"])
	}
}

// TestDBImage_FallsBackWhenUnpinned locks the dbImageFor selector:
// production deploys thread a digest-pinned ref via ResolveDBImage,
// but tests + unpinned local builds get `docker.io/nvoi/db:<tag>`.
func TestDBImage_FallsBackWhenUnpinned(t *testing.T) {
	req := DatabaseRequest{}
	if got := dbImageFor(req); got != DBImageRepo+":"+DBImageTag {
		t.Errorf("dbImageFor(empty) = %q, want %q", got, DBImage())
	}
	req.DBImageRef = "docker.io/nvoi/db@sha256:abc123"
	if got := dbImageFor(req); got != "docker.io/nvoi/db@sha256:abc123" {
		t.Errorf("dbImageFor(pinned) = %q, want pinned", got)
	}
}

// TestBuildBackupCredsSecretData_DefaultsRegion locks the AUTO region
// default (R2 uses "auto"; AWS requires a real region; Scaleway uses
// fr-par etc.). Empty Region in creds maps to "auto" — the most
// permissive default; an operator who needs strict region binding
// passes it explicitly.
func TestBuildBackupCredsSecretData_DefaultsRegion(t *testing.T) {
	data := BuildBackupCredsSecretData("nvoi-x-y-db-z-backups", BucketCredentials{
		Endpoint: "https://acc.r2.cloudflarestorage.com/",
		AccessKeyID: "A", SecretAccessKey: "S",
	})
	if data["AWS_REGION"] != "auto" {
		t.Errorf("AWS_REGION = %q, want auto (default)", data["AWS_REGION"])
	}
	if data["BUCKET_ENDPOINT"] != "https://acc.r2.cloudflarestorage.com" {
		t.Errorf("trailing slash not trimmed from endpoint: %q", data["BUCKET_ENDPOINT"])
	}
	if data["BUCKET_NAME"] != "nvoi-x-y-db-z-backups" {
		t.Errorf("BUCKET_NAME = %q", data["BUCKET_NAME"])
	}
}
