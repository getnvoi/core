// Package state owns the terraform-remote-state lifecycle: provision
// the per-(app, env) bucket via the configured BucketProvider and
// resolve the S3-compatible credentials terraform's `backend "s3" {}`
// block needs.
//
// Two responsibilities, one orchestration:
//
//  1. EnsureBucket — idempotent. The first deploy creates
//     `nvoi-{app}-{env}-tfstate`; every subsequent deploy is a no-op.
//
//  2. Credentials — returns S3-compatible access details. Used by
//     compile to render the backend block and by deploy.go to inject
//     into terraform-exec's subprocess env so terraform can read the
//     state.
package state

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/naming"
	"github.com/getnvoi/core/internal/providers"
)

// Backend is the resolved terraform-state backend config — bucket
// name + S3-compatible creds. Stamped on runtime.Runtime by the cmd/
// boundary; consumed by the hetzner emitter (renders backend block)
// and the runner (sets AWS_* env vars on the tf subprocess).
type Backend struct {
	Bucket    string
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
}

// Configure provisions the state bucket (idempotent) and returns the
// Backend describing it. Called once at the cmd/ boundary, before
// runtime.Build.
func Configure(ctx context.Context, app, env string, bp providers.BucketProvider, lg log.Log) (Backend, error) {
	if err := bp.ValidateCredentials(ctx); err != nil {
		return Backend{}, fmt.Errorf("validate bucket creds: %w", err)
	}

	bucket := naming.StateBucket(app, env)
	lg.Step("state-bucket")
	lg.Info(fmt.Sprintf("ensuring state bucket %s...", bucket))
	if err := bp.EnsureBucket(ctx, bucket); err != nil {
		return Backend{}, fmt.Errorf("ensure state bucket: %w", err)
	}

	creds, err := bp.Credentials(ctx)
	if err != nil {
		return Backend{}, fmt.Errorf("bucket credentials: %w", err)
	}

	return Backend{
		Bucket:    bucket,
		Endpoint:  creds.Endpoint,
		Region:    creds.Region,
		AccessKey: creds.AccessKeyID,
		SecretKey: creds.SecretAccessKey,
	}, nil
}
