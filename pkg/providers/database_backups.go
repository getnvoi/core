// Shared database backup + restore pipeline.
//
// Every DatabaseProvider produces backups the same way: gzipped logical
// dumps land in `nvoi-{app}-{env}-db-{name}-backups`, pushed by a
// CronJob (scheduled) or a one-shot Job (manual via `nvoi database
// backup now`). Restore is symmetric: a one-shot Job pulls an object,
// gunzips, and pipes into the engine's native restore tool against
// $DATABASE_URL. Pull/put mechanics vary by engine; direction flips
// via MODE env var. ListBackups / DownloadBackup walk the bucket
// directly and are engine-agnostic.
//
// The uniform image (`docker.io/nvoi/db:<cli-version>`) carries
// pg_dump + psql + mysqldump + mysql + gzip + a sigv4-aware client
// and dispatches based on `MODE` (backup | restore) and `ENGINE`.
// Image source is `cmd/db/`; publish workflow lives in
// `.github/workflows/release.yml`.
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/utils/s3"
)

// DBImageRepo is the registry path of the uniform backup + restore
// container image. Built from cmd/db/Dockerfile, published to Docker
// Hub on every `v*` tag by .github/workflows/release.yml. Public repo,
// no auth required for pull.
const DBImageRepo = "docker.io/nvoi/db"

// DBImageTag is the tag nvoi appends to DBImageRepo. Overridden at
// build time:
//
//	-ldflags "-X github.com/getnvoi/core/pkg/providers.DBImageTag=v1.2.3"
//
// set in .github/workflows/release.yml on every `v*` tag. Default
// "latest" makes local builds pull the most recent stable image —
// release.yml publishes both `:vX.Y.Z` and `:latest` per release.
// Tagged builds inject the pinned tag so prod CLI and image stay in
// lockstep.
var DBImageTag = "latest"

// DBImage is the unresolved (`:tag`) reference. Used as a fallback
// when DatabaseRequest.DBImageRef is empty — tests that build Jobs
// without exercising a live registry, or callers that explicitly
// don't need digest-pinning. Production reconcile always resolves
// via ResolveDBImage and threads the digest-pinned ref through
// req.DBImageRef.
func DBImage() string {
	return DBImageRepo + ":" + DBImageTag
}

// dbImageFor returns req.DBImageRef when set, else DBImage(). Single
// source of "what image string lands on the CronJob/Job" so the
// fallback rule lives in one place.
func dbImageFor(req DatabaseRequest) string {
	if req.DBImageRef != "" {
		return req.DBImageRef
	}
	return DBImage()
}

// dbCredsEnv returns the explicit env-var mappings the cmd/db image's
// entrypoint reads, sourcing each from the credentials Secret named
// `secret`. Used by BuildBackupCronJob and BuildRestoreJob.
//
// Why explicit Name → SecretKeyRef.Key (not `envFrom prefix=DB_`):
// Kubernetes envFrom binds each Secret key as an env var with name
// = prefix + key, *verbatim* — keys are NOT uppercased. The
// credentials Secret has lowercase keys (`url`, `host`, …) for
// Go-side reads, so envFrom-prefix would produce `DB_url` (lowercase)
// and cmd/db's `mustEnv("DB_URL")` would always fail. Explicit
// mapping keeps the contract.
//
// Why every field is bound (not just DB_URL): cmd/db reads each
// field directly from its env var; no DSN re-parse. Lossy round-trip
// (port + query options dropped) is avoided.
//
// NOTE: this is the cmd/db image's INTERNAL env contract — distinct
// from the canonical DATABASE_* env vars injected into CONSUMING
// services. See pkg/workload (env wiring for services.X.databases)
// for the latter.
func dbCredsEnv(secret string) []corev1.EnvVar {
	ref := func(envName, secretKey string) corev1.EnvVar {
		return corev1.EnvVar{
			Name: envName,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secret},
					Key:                  secretKey,
				},
			},
		}
	}
	return []corev1.EnvVar{
		ref("DB_URL", "url"),
		ref("DB_HOST", "host"),
		ref("DB_PORT", "port"),
		ref("DB_USER", "user"),
		ref("DB_PASSWORD", "password"),
		ref("DB_DATABASE", "database"),
		ref("DB_SSLMODE", "sslmode"),
	}
}

// ResolveDBImage returns the digest-pinned reference
// (`docker.io/nvoi/db@sha256:<digest>`) for DBImageTag by HEAD-ing
// the Docker Hub manifest endpoint. Reconcile calls this once per
// deploy and threads the result into every DatabaseRequest, so
// kubelet sees a fresh image string whenever the underlying image
// content changed. `:latest` alone never invalidates the kubelet
// cache — a single bad push leaves backup pods in ImagePullBackOff
// indefinitely.
//
// Failure modes (all hard errors — better to fail the deploy than
// apply a CronJob whose image we couldn't verify):
//   - tag not found on Docker Hub → push silently failed, pre-deploy
//   - registry unreachable      → fix internet, then redeploy
//   - missing digest header     → registry implementation oddity
//
// Package var so tests can stub the registry round-trip without
// hitting the network.
var ResolveDBImage = registryResolveDBImage

func registryResolveDBImage(ctx context.Context) (string, error) {
	digest, err := registryDigest(ctx, "nvoi/db", DBImageTag)
	if err != nil {
		return "", fmt.Errorf("resolve %s:%s digest: %w", DBImageRepo, DBImageTag, err)
	}
	return DBImageRepo + "@" + digest, nil
}

// registryDigest queries Docker Hub for the current
// `Docker-Content-Digest` of <repo>:<tag>. Anonymous, public-repo
// only — nvoi/db is published as a public image. Two-step protocol:
//
//  1. GET auth.docker.io/token?service=registry.docker.io&scope=...
//     → bearer token
//  2. HEAD registry-1.docker.io/v2/<repo>/manifests/<tag> with the
//     Bearer token + Accept covering OCI image-index AND Docker
//     manifest-list (buildx pushes OCI; older registries / buildx
//     versions may push Docker — both shapes accepted).
func registryDigest(ctx context.Context, repo, tag string) (string, error) {
	tokenURL := fmt.Sprintf(
		"https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s:pull",
		repo,
	)
	tokenReq, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", fmt.Errorf("build token request: %w", err)
	}
	tokenResp, err := http.DefaultClient.Do(tokenReq)
	if err != nil {
		return "", fmt.Errorf("fetch token: %w", err)
	}
	defer tokenResp.Body.Close()
	if tokenResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint returned %d", tokenResp.StatusCode)
	}
	var tokenBody struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenBody); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if tokenBody.Token == "" {
		return "", fmt.Errorf("empty token")
	}

	manifestURL := fmt.Sprintf("https://registry-1.docker.io/v2/%s/manifests/%s", repo, tag)
	manReq, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build manifest request: %w", err)
	}
	manReq.Header.Set("Authorization", "Bearer "+tokenBody.Token)
	manReq.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	manResp, err := http.DefaultClient.Do(manReq)
	if err != nil {
		return "", fmt.Errorf("HEAD manifest: %w", err)
	}
	defer manResp.Body.Close()
	if manResp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("tag %s:%s not found — was the image actually pushed? (try `docker buildx imagetools inspect %s:%s`)",
			repo, tag, repo, tag)
	}
	if manResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("manifest %s:%s returned %d", repo, tag, manResp.StatusCode)
	}
	digest := manResp.Header.Get("Docker-Content-Digest")
	if digest == "" {
		return "", fmt.Errorf("missing Docker-Content-Digest header for %s:%s", repo, tag)
	}
	return digest, nil
}

// BuildBackupCronJob returns the uniform CronJob that dumps a database
// and uploads the gzipped result to the bucket named in req.Bucket.
// Every DatabaseProvider whose Spec.Backup is set embeds this CronJob
// in the Workloads it returns from Reconcile.
//
// Requirements on req:
//   - Spec.Backup.Schedule is a valid cron expression.
//   - Bucket carries the S3-compatible endpoint/key material.
//   - CredentialsSecretName exists and contains url/host/port/user/
//     password/database/sslmode (the canonical key shape).
//   - BackupCredsSecretName exists and contains BUCKET_* + AWS_*.
//
// The CronJob's Command is the image's default entrypoint — the image
// reads ENGINE + DB_* + BUCKET_* from the env and picks the right
// dump tool.
func BuildBackupCronJob(req DatabaseRequest) *batchv1.CronJob {
	labels := map[string]string{}
	for k, v := range req.Labels {
		labels[k] = v
	}

	backoff := int32(2)
	successHistory := int32(3)
	failureHistory := int32(3)

	return &batchv1.CronJob{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "CronJob"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.BackupName,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.CronJobSpec{
			Schedule:                   req.Spec.Backup.Schedule,
			SuccessfulJobsHistoryLimit: &successHistory,
			FailedJobsHistoryLimit:     &failureHistory,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					BackoffLimit: &backoff,
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{Labels: labels},
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{{
								Name:  "backup",
								Image: dbImageFor(req),
								Env: append(
									[]corev1.EnvVar{
										{Name: "ENGINE", Value: req.Spec.Engine},
										{Name: "DATABASE_NAME", Value: req.Name},
										{Name: "DATABASE_FULL_NAME", Value: req.FullName},
									},
									// DB_* mapping — explicit SecretKeyRef per
									// key. envFrom-prefix would produce
									// lowercase env vars (Secret keys aren't
									// uppercased by envFrom); cmd/db reads
									// uppercase DB_* and would fail.
									dbCredsEnv(req.CredentialsSecretName)...,
								),
								EnvFrom: []corev1.EnvFromSource{
									// Bucket creds: BUCKET_* + AWS_* are
									// stored uppercase in BackupCreds Secret
									// (see BuildBackupCredsSecretData) so
									// envFrom-no-prefix is the right path.
									{
										SecretRef: &corev1.SecretEnvSource{
											LocalObjectReference: corev1.LocalObjectReference{Name: req.BackupCredsSecretName},
										},
									},
								},
							}},
						},
					},
				},
			},
		},
	}
}

// BuildRestoreJob returns a one-shot Job that replays a bucket-resident
// backup artifact into the database. Mirrors BuildBackupCronJob's pod
// spec — same image, same envFrom Secrets — with two additions:
//
//   - MODE=restore flips the image's dispatch to the restore pipeline.
//   - BACKUP_KEY names the bucket object to pull.
//
// Used by RunRestoreJob below, which is what every DatabaseProvider's
// Restore method calls. Single source of truth for the restore Job's
// shape; engine-specificity (psql vs mysql) lives in the image's
// dispatch.
//
// The Job is named deterministically with a unix timestamp suffix so
// concurrent restores from different operators don't collide. The
// caller (RunRestoreJob) waits for the Job to succeed before
// returning.
func BuildRestoreJob(req DatabaseRequest, backupKey string) *batchv1.Job {
	labels := map[string]string{
		"nvoi/restore-of": req.Name,
	}
	for k, v := range req.Labels {
		if _, exists := labels[k]; !exists {
			labels[k] = v
		}
	}

	// Restores don't auto-retry — a failed restore leaves the DB in
	// an unknown state; the operator decides what to do.
	backoff := int32(0)

	// Job name embeds a unix timestamp so concurrent restore calls
	// (or a retry after a crash) don't collide on the same name.
	jobName := fmt.Sprintf("%s-restore-%d", req.FullName, time.Now().Unix())

	return &batchv1.Job{
		TypeMeta: metav1.TypeMeta{APIVersion: "batch/v1", Kind: "Job"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: req.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{{
						Name:  "restore",
						Image: dbImageFor(req),
						Env: append(
							[]corev1.EnvVar{
								{Name: "MODE", Value: "restore"},
								{Name: "BACKUP_KEY", Value: backupKey},
								{Name: "ENGINE", Value: req.Spec.Engine},
								{Name: "DATABASE_NAME", Value: req.Name},
								{Name: "DATABASE_FULL_NAME", Value: req.FullName},
							},
							dbCredsEnv(req.CredentialsSecretName)...,
						),
						EnvFrom: []corev1.EnvFromSource{
							{
								SecretRef: &corev1.SecretEnvSource{
									LocalObjectReference: corev1.LocalObjectReference{Name: req.BackupCredsSecretName},
								},
							},
						},
					}},
				},
			},
		},
	}
}

// RunRestoreJob applies the restore Job and blocks until it completes.
// Shared across every DatabaseProvider — each provider's Restore
// method is a one-liner calling this helper. On Job failure,
// WaitForJob returns an error with the pod's recent logs attached,
// which is what the operator sees on the CLI.
func RunRestoreJob(ctx context.Context, req DatabaseRequest, backupKey string) error {
	if req.Kube == nil {
		return fmt.Errorf("restore requires kube client (req.Kube is nil)")
	}
	if req.BackupCredsSecretName == "" || req.Bucket == nil {
		return fmt.Errorf("restore requires providers.storage + a backup bucket (did providers.storage get unset between backup and restore?)")
	}
	job := BuildRestoreJob(req, backupKey)
	scope := kube.Scope{Namespace: req.Namespace, Owner: kube.OwnerDatabases}
	if err := req.Kube.ApplyOwned(ctx, scope, job); err != nil {
		return fmt.Errorf("apply restore job %s: %w", job.Name, err)
	}
	var emitter kube.ProgressEmitter
	if req.Log != nil {
		emitter = logEmitter{log: req.Log}
	}
	if err := req.Kube.WaitForJob(ctx, req.Namespace, job.Name, emitter); err != nil {
		return fmt.Errorf("restore job %s: %w", job.Name, err)
	}
	return nil
}

// logEmitter adapts log.Log to kube.ProgressEmitter — kube/ doesn't
// import log/, but the database verbs do, and they want progress
// updates to flow through the canonical logger.
type logEmitter struct{ log interface{ Info(msg string) } }

func (e logEmitter) Progress(msg string) { e.log.Info(msg) }

// BucketListBackups enumerates every dump in the database's backup
// bucket. Used by every DatabaseProvider — implementations delegate
// to this helper instead of each rolling their own object-store loop.
// Kind is always "dump" (uniform pipeline = uniform artifact shape).
func BucketListBackups(_ context.Context, req DatabaseRequest) ([]BackupRef, error) {
	if req.Bucket == nil {
		return nil, fmt.Errorf("backups require providers.storage + a backup bucket")
	}
	objs, err := s3.ListObjects(
		req.Bucket.Credentials.Endpoint,
		req.Bucket.Credentials.AccessKeyID,
		req.Bucket.Credentials.SecretAccessKey,
		req.Bucket.Name,
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("list backups for %s: %w", req.Name, err)
	}
	out := make([]BackupRef, 0, len(objs))
	for _, o := range objs {
		out = append(out, BackupRef{
			ID:        o.Key,
			CreatedAt: o.LastModified,
			SizeBytes: o.Size,
			Kind:      "dump",
		})
	}
	return out, nil
}

// BucketDownloadBackup streams a single dump to the writer. Shared
// implementation across every DatabaseProvider — the pipeline's
// uniformity makes list/download provider-agnostic.
func BucketDownloadBackup(_ context.Context, req DatabaseRequest, id string, w io.Writer) error {
	if req.Bucket == nil {
		return fmt.Errorf("backups require providers.storage + a backup bucket")
	}
	rc, _, _, err := s3.GetStream(
		req.Bucket.Credentials.Endpoint,
		req.Bucket.Credentials.AccessKeyID,
		req.Bucket.Credentials.SecretAccessKey,
		req.Bucket.Name,
		id,
	)
	if err != nil {
		return fmt.Errorf("download backup %s/%s: %w", req.Bucket.Name, id, err)
	}
	defer rc.Close()
	if _, err := io.Copy(w, rc); err != nil {
		return fmt.Errorf("stream backup %s: %w", id, err)
	}
	return nil
}

// BuildBackupCredsSecretData materializes the bucket credentials into
// the env-var shape the backup image expects. Kept here (not in the
// bucket package) because this is the one place nvoi crosses the
// boundary from "bucket credentials" to "backup image contract" —
// renaming a key here breaks the image's entrypoint script and
// nothing else.
func BuildBackupCredsSecretData(bucketName string, creds BucketCredentials) map[string]string {
	region := creds.Region
	if region == "" {
		region = "auto"
	}
	return map[string]string{
		"BUCKET_ENDPOINT":       strings.TrimRight(creds.Endpoint, "/"),
		"BUCKET_NAME":           bucketName,
		"AWS_ACCESS_KEY_ID":     creds.AccessKeyID,
		"AWS_SECRET_ACCESS_KEY": creds.SecretAccessKey,
		"AWS_REGION":            region,
	}
}
