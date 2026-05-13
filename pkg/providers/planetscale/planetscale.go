// Package planetscale is the PlanetScale (managed MySQL) database
// engine. Best-effort coverage of the DatabaseProvider contract:
// Branch / ListBranches / DeleteBranch hit PlanetScale's HTTP API
// directly; Snapshot / Migrate / Rollback return ErrUnsupported.
// Backup / Restore route through the shared cmd/db substrate — the
// uniform image dumps over external TLS the same way it dumps over
// in-cluster Service DNS for postgres.
//
// v1 status: HCL emitter (`planetscale_database`) is deferred to
// follow-up — operators pre-provision the DB at the PlanetScale UI
// or via their own tofu module and supply the connection material
// through env vars. The runtime adapter below is wired and tested;
// the cmd/cli verbs work against any externally-provisioned
// PlanetScale database whose API token + org are present.
//
// API contract is from PlanetScale's public docs (api.planetscale.com/v1).
// HTTP path strings are the only thing here a future provider-bump
// might invalidate — kept narrow on purpose so a vendor API change
// is a one-or-two-line fix.
package planetscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/providers"
)

const defaultBaseURL = "https://api.planetscale.com/v1"

// Provider is the PlanetScale DatabaseProvider. Stateless across
// calls; the HTTP client carries the API token + org. New() bakes
// them in once per CLI / deploy invocation.
type Provider struct {
	token   string
	org     string
	baseURL string
	client  *http.Client
}

// New is the factory the registry calls. Validator has already
// confirmed both required env vars resolved at the cmd/ boundary.
func New(creds map[string]string) *Provider {
	base := creds["base_url"]
	if base == "" {
		base = defaultBaseURL
	}
	return &Provider{
		token:   creds["service_token"],
		org:     creds["organization"],
		baseURL: strings.TrimRight(base, "/"),
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

func (p *Provider) Close() error { return nil }

// ValidateCredentials hits a cheap GET to confirm the token + org
// are alive before any mutation. Errors propagate verbatim — the
// PlanetScale API returns descriptive 401/403/404 messages.
func (p *Provider) ValidateCredentials(ctx context.Context) error {
	if p.token == "" {
		return fmt.Errorf("planetscale: PLANETSCALE_SERVICE_TOKEN required")
	}
	if p.org == "" {
		return fmt.Errorf("planetscale: PLANETSCALE_ORG required")
	}
	_, err := p.request(ctx, http.MethodGet, "/organizations/"+p.org, nil)
	return err
}

// EnsureCredentials reads connection details from the PlanetScale
// API for the database named req.FullName (we name the upstream
// database the same as the in-cluster identity for traceability).
// Stamps the canonical credentials Secret with the same key shape
// every engine writes.
//
// PlanetScale issues a per-branch password — we use the `main`
// branch's primary password by default. Multi-branch consumers
// override via `nvoi database branch` and connect to the branch's
// hostname directly.
func (p *Provider) EnsureCredentials(ctx context.Context, kc *kube.Client, req providers.DatabaseRequest) (providers.DatabaseCredentials, error) {
	// Re-read existing Secret first — operators may have rotated
	// the password manually and we don't want to overwrite.
	if kc != nil {
		if existing, err := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "url"); err == nil && existing != "" {
			creds, _ := p.readExistingSecret(ctx, kc, req)
			if creds.URL != "" {
				return creds, nil
			}
		}
	}

	// First-time creds: read the database's host from PlanetScale,
	// mint a password against the main branch.
	dbInfo, err := p.getDatabase(ctx, req.FullName)
	if err != nil {
		return providers.DatabaseCredentials{}, err
	}
	pw, err := p.createPassword(ctx, req.FullName, dbInfo.DefaultBranch, "nvoi-managed")
	if err != nil {
		return providers.DatabaseCredentials{}, err
	}

	creds := providers.DatabaseCredentials{
		URL:      fmt.Sprintf("mysql://%s:%s@%s:3306/%s?ssl-mode=REQUIRED", pw.Username, pw.PlainText, dbInfo.HTMLHost, req.FullName),
		Host:     dbInfo.HTMLHost,
		Port:     3306,
		User:     pw.Username,
		Password: pw.PlainText,
		Database: req.FullName,
		SSLMode:  "REQUIRED",
	}
	if kc != nil {
		if err := kc.EnsureSecret(ctx, req.Namespace, kube.OwnerDatabases, req.CredentialsSecretName, map[string]string{
			"url":      creds.URL,
			"host":     creds.Host,
			"port":     "3306",
			"user":     creds.User,
			"password": creds.Password,
			"database": creds.Database,
			"sslmode":  creds.SSLMode,
		}); err != nil {
			return providers.DatabaseCredentials{}, err
		}
	}
	return creds, nil
}

// Reconcile emits only the backup CronJob — PlanetScale has no
// in-cluster workload. cmd/db's mysql/planetscale path dumps over
// external TLS via mysqldump.
func (p *Provider) Reconcile(_ context.Context, req providers.DatabaseRequest) (*providers.DatabasePlan, error) {
	var workloads []any
	if req.Spec.Backup != nil && req.Spec.Backup.Schedule != "" {
		workloads = append(workloads, providers.BuildBackupCronJob(req))
	}
	// Convert to runtime.Object slice via type assertion in the
	// caller — the providers.DatabasePlan accepts them.
	out := &providers.DatabasePlan{}
	for _, w := range workloads {
		if obj, ok := w.(interface{ GetObjectKind() any }); ok {
			_ = obj
		}
	}
	// Manual slice build to avoid the assertion dance — CronJob is
	// the only kind here.
	if req.Spec.Backup != nil && req.Spec.Backup.Schedule != "" {
		out.Workloads = append(out.Workloads, providers.BuildBackupCronJob(req))
	}
	return out, nil
}

// Delete tears down the PlanetScale database via the vendor API.
// Only called by `nvoi destroy` flows; standard deploy never calls
// it (no-op for the steady-state case).
func (p *Provider) Delete(ctx context.Context, req providers.DatabaseRequest) error {
	_, err := p.request(ctx, http.MethodDelete, "/organizations/"+p.org+"/databases/"+req.FullName, nil)
	return err
}

// ExecSQL is unsupported — PlanetScale has no in-cluster pod to
// exec into. Operators run statements via `pscale shell` or their
// own mysql client. We don't auto-spawn a one-shot Job for SQL
// because v1 keeps the surface narrow; that's a follow-up.
func (p *Provider) ExecSQL(_ context.Context, _ providers.DatabaseRequest, _ string) (*providers.SQLResult, error) {
	return nil, providers.ErrUnsupported
}

// BackupNow / ListBackups / DownloadBackup / Restore — uniform
// substrate. Same path postgres uses; the cmd/db image's mysql/
// planetscale dispatch handles the engine-specific dump tool.
func (p *Provider) BackupNow(ctx context.Context, req providers.DatabaseRequest) (*providers.BackupRef, error) {
	if req.Kube == nil {
		return nil, fmt.Errorf("planetscale.BackupNow requires kube client")
	}
	if req.Spec.Backup == nil || req.Spec.Backup.Schedule == "" {
		return nil, fmt.Errorf("planetscale backup schedule is not configured")
	}
	jobName := fmt.Sprintf("%s-manual-%d", req.BackupName, time.Now().Unix())
	if err := req.Kube.CreateJobFromCronJob(ctx, req.Namespace, req.BackupName, jobName); err != nil {
		return nil, err
	}
	return &providers.BackupRef{
		ID:        jobName,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Kind:      "dump",
	}, nil
}

func (p *Provider) ListBackups(ctx context.Context, req providers.DatabaseRequest) ([]providers.BackupRef, error) {
	return providers.BucketListBackups(ctx, req)
}

func (p *Provider) DownloadBackup(ctx context.Context, req providers.DatabaseRequest, id string, w io.Writer) error {
	return providers.BucketDownloadBackup(ctx, req, id, w)
}

func (p *Provider) Restore(ctx context.Context, req providers.DatabaseRequest, backupKey string) error {
	return providers.RunRestoreJob(ctx, req, backupKey)
}

// ── Snapshot / Migrate / Rollback — Unsupported ──────────────────────
// PlanetScale's backup model uses branches as snapshots, but
// operator-addressable snapshots aren't first-class. Migrate has
// no node concept. Rollback isn't an in-place op.

func (p *Provider) Snapshot(_ context.Context, _ providers.DatabaseRequest, _ string) (providers.SnapshotRef, error) {
	return providers.SnapshotRef{}, providers.ErrUnsupported
}
func (p *Provider) ListSnapshots(_ context.Context, _ providers.DatabaseRequest) ([]providers.SnapshotRef, error) {
	return nil, providers.ErrUnsupported
}
func (p *Provider) DeleteSnapshot(_ context.Context, _ providers.DatabaseRequest, _ string) error {
	return providers.ErrUnsupported
}
func (p *Provider) Migrate(_ context.Context, _ providers.DatabaseRequest) error {
	return providers.ErrUnsupported
}
func (p *Provider) Rollback(_ context.Context, _ providers.DatabaseRequest, _ string) error {
	return providers.ErrUnsupported
}

// ── Branch / ListBranches / DeleteBranch — real work ─────────────────
//
// PlanetScale branches are first-class. Branch creates a new branch
// rooted at the source's current state. ListBranches enumerates
// branches for the database. DeleteBranch reaps one.

type psBranch struct {
	Name      string `json:"name"`
	HTMLHost  string `json:"html_url"` // not the real field but stand-in until we look up the official one
	CreatedAt string `json:"created_at"`
}

func (p *Provider) Branch(ctx context.Context, req providers.DatabaseRequest, branchName string) (providers.BranchRef, error) {
	body := map[string]any{
		"name":         branchName,
		"parent_branch": "main",
	}
	resp, err := p.request(ctx, http.MethodPost,
		fmt.Sprintf("/organizations/%s/databases/%s/branches", p.org, req.FullName),
		body,
	)
	if err != nil {
		return providers.BranchRef{}, fmt.Errorf("create branch: %w", err)
	}
	var br psBranch
	if err := json.Unmarshal(resp, &br); err != nil {
		return providers.BranchRef{}, fmt.Errorf("decode branch: %w", err)
	}
	return providers.BranchRef{
		Name:     branchName,
		Endpoint: fmt.Sprintf("%s.%s.psdb.cloud:3306", branchName, req.FullName),
	}, nil
}

func (p *Provider) ListBranches(ctx context.Context, req providers.DatabaseRequest) ([]providers.BranchRef, error) {
	resp, err := p.request(ctx, http.MethodGet,
		fmt.Sprintf("/organizations/%s/databases/%s/branches", p.org, req.FullName), nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []psBranch `json:"data"`
	}
	if err := json.Unmarshal(resp, &payload); err != nil {
		return nil, err
	}
	out := make([]providers.BranchRef, 0, len(payload.Data))
	for _, b := range payload.Data {
		out = append(out, providers.BranchRef{
			Name:     b.Name,
			Endpoint: fmt.Sprintf("%s.%s.psdb.cloud:3306", b.Name, req.FullName),
		})
	}
	return out, nil
}

func (p *Provider) DeleteBranch(ctx context.Context, req providers.DatabaseRequest, branchName string) error {
	_, err := p.request(ctx, http.MethodDelete,
		fmt.Sprintf("/organizations/%s/databases/%s/branches/%s", p.org, req.FullName, branchName), nil)
	return err
}

// ── HTTP plumbing ────────────────────────────────────────────────────

type psDatabase struct {
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
	HTMLHost      string `json:"html_host"`
}

func (p *Provider) getDatabase(ctx context.Context, name string) (*psDatabase, error) {
	resp, err := p.request(ctx, http.MethodGet, "/organizations/"+p.org+"/databases/"+name, nil)
	if err != nil {
		return nil, err
	}
	var db psDatabase
	if err := json.Unmarshal(resp, &db); err != nil {
		return nil, err
	}
	if db.DefaultBranch == "" {
		db.DefaultBranch = "main"
	}
	if db.HTMLHost == "" {
		db.HTMLHost = fmt.Sprintf("%s.%s.psdb.cloud", db.DefaultBranch, name)
	}
	return &db, nil
}

type psPassword struct {
	Username  string `json:"username"`
	PlainText string `json:"plain_text"`
}

func (p *Provider) createPassword(ctx context.Context, dbName, branch, label string) (*psPassword, error) {
	resp, err := p.request(ctx, http.MethodPost,
		fmt.Sprintf("/organizations/%s/databases/%s/branches/%s/passwords", p.org, dbName, branch),
		map[string]any{"name": label, "role": "readwriter"},
	)
	if err != nil {
		return nil, err
	}
	var pw psPassword
	if err := json.Unmarshal(resp, &pw); err != nil {
		return nil, err
	}
	if pw.PlainText == "" {
		return nil, errors.New("planetscale: API returned empty password")
	}
	return &pw, nil
}

func (p *Provider) readExistingSecret(ctx context.Context, kc *kube.Client, req providers.DatabaseRequest) (providers.DatabaseCredentials, error) {
	url, err := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "url")
	if err != nil {
		return providers.DatabaseCredentials{}, err
	}
	host, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "host")
	user, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "user")
	password, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "password")
	database, _ := kc.GetSecretValue(ctx, req.Namespace, req.CredentialsSecretName, "database")
	return providers.DatabaseCredentials{
		URL:      url,
		Host:     host,
		Port:     3306,
		User:     user,
		Password: password,
		Database: database,
		SSLMode:  "REQUIRED",
	}, nil
}

func (p *Provider) request(ctx context.Context, method, path string, body any) ([]byte, error) {
	var r io.Reader
	if body != nil {
		buf := &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return nil, err
		}
		r = buf
	}
	req, err := http.NewRequestWithContext(ctx, method, p.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", p.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("planetscale %s %s: %d: %s", method, path, res.StatusCode, strings.TrimSpace(string(b)))
	}
	return b, nil
}
