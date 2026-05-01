// Package providers holds the cross-provider abstractions: BucketProvider
// (object storage), and the registry that maps a YAML provider name
// (e.g. "cloudflare") to its concrete implementation.
//
// Each provider's package (cloudflare, aws, scaleway, …) lives as a
// sibling under this directory and registers itself via init().
package providers

import "context"

// BucketProvider abstracts object storage. Used today only for the
// terraform state backend (provisioned per (app, env) and managed by
// internal/state). When workload-side `storage:` lands, the same
// provider serves both — bucket per app + bucket for state.
//
// V0 surface is intentionally narrow — Ensure/Delete/Credentials/Validate.
// CORS, lifecycle, listing all buckets etc. lift in when the workload
// layer needs them.
type BucketProvider interface {
	ValidateCredentials(ctx context.Context) error
	EnsureBucket(ctx context.Context, name string) error
	DeleteBucket(ctx context.Context, name string) error

	// Credentials returns S3-compatible access details for the
	// provider's bucket service. Used by internal/state to inject into
	// terraform's `backend "s3"` block.
	Credentials(ctx context.Context) (BucketCredentials, error)
}

// BucketCredentials are the S3-compatible access details every
// BucketProvider produces. Pluggable into terraform's s3 backend
// (R2, Scaleway, AWS, MinIO, …) regardless of the underlying API.
type BucketCredentials struct {
	Endpoint        string // e.g. https://<acct>.r2.cloudflarestorage.com
	AccessKeyID     string
	SecretAccessKey string
	Region          string // "auto" for R2, "eu-west-3" for AWS, etc.
}
