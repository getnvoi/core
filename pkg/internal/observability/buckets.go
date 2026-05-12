package observability

// buckets.go provisions the two object-storage buckets the
// observability stack needs (logs for Loki, metrics for Thanos) and
// resolves their S3-compatible credentials. Uses the same
// BucketProvider abstraction that pkg/state uses for the tfstate
// bucket — single credential surface, no new boundary.
//
// Buckets persist across deploys; they're deleted only by `nvoi
// destroy`. Same lifecycle as the tfstate bucket.

import (
	"context"
	"fmt"

	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/runtime"
)

// BucketCreds is the resolved S3-compatible credentials + bucket names
// every observability manifest builder consumes. Built once per deploy
// (EnsureBuckets), then threaded into builders that need it (Loki +
// Thanos sidecar / store). Same shape both stacks expect — single
// source of truth.
type BucketCreds struct {
	Endpoint      string // e.g. https://<acct>.r2.cloudflarestorage.com
	Region        string // "auto" for R2
	AccessKey     string
	SecretKey     string
	LogsBucket    string // nvoi-{app}-{env}-logs
	MetricsBucket string // nvoi-{app}-{env}-metrics
}

// EnsureBuckets provisions both observability buckets (idempotent)
// and returns their resolved credentials. Mirrors state.Configure:
// validate creds → ensure bucket(s) → fetch credentials. Reuses the
// operator's already-resolved bucket provider (looked up via
// providers.ResolveBucket) so credentials come from the same env-var
// resolution that produced rt.Backend.
//
// Returns an error when cfg.Providers.Storage is empty — validation
// in pkg/config/validate.go already rejects that case before reaching
// here, but the guard keeps callers honest if validation ever changes.
func EnsureBuckets(ctx context.Context, rt *runtime.Runtime, lg log.Log) (BucketCreds, error) {
	if rt.Cfg.Providers.Storage == "" {
		return BucketCreds{}, fmt.Errorf("monitor: providers.storage required for Thanos + Loki buckets")
	}
	if rt.Backend == nil {
		return BucketCreds{}, fmt.Errorf("monitor: runtime state backend not configured (cfg.Providers.Storage set but rt.Backend nil)")
	}

	logsName := naming.ObservabilityLogsBucket(rt.Cfg.App, rt.Cfg.Env)
	metricsName := naming.ObservabilityMetricsBucket(rt.Cfg.App, rt.Cfg.Env)

	// Resolve the same BucketProvider state.Configure used. Credentials
	// are operator-wide and already resolved at the cmd/cli boundary
	// (internal/cli.PrepareRuntime), so re-resolution here is cheap.
	// We thread them through Runtime.Backend rather than re-reading
	// env vars — keeps the boundary discipline (no os.* outside cmd/).
	bp, err := resolveBucketProvider(rt)
	if err != nil {
		return BucketCreds{}, fmt.Errorf("monitor: resolve bucket provider: %w", err)
	}

	lg.Step("monitor-bucket-logs")
	lg.Info(fmt.Sprintf("ensuring logs bucket %s...", logsName))
	if err := bp.EnsureBucket(ctx, logsName); err != nil {
		return BucketCreds{}, fmt.Errorf("ensure logs bucket: %w", err)
	}

	lg.Step("monitor-bucket-metrics")
	lg.Info(fmt.Sprintf("ensuring metrics bucket %s...", metricsName))
	if err := bp.EnsureBucket(ctx, metricsName); err != nil {
		return BucketCreds{}, fmt.Errorf("ensure metrics bucket: %w", err)
	}

	return BucketCreds{
		Endpoint:      rt.Backend.Endpoint,
		Region:        rt.Backend.Region,
		AccessKey:     rt.Backend.AccessKey,
		SecretKey:     rt.Backend.SecretKey,
		LogsBucket:    logsName,
		MetricsBucket: metricsName,
	}, nil
}

// resolveBucketProvider rebuilds the BucketProvider from the resolved
// backend credentials on rt. Kept here rather than on Runtime because
// providers are constructed per-need (BucketFactory is the registry
// pattern) and runtime stays a data bag.
//
// Note: the credentials on rt.Backend already came from a successful
// state.Configure round-trip, so re-resolving them against the
// provider's schema is a re-binding, not a re-validation. The
// BucketProvider re-validates credentials on its first API call
// (ValidateCredentials path inside EnsureBucket).
func resolveBucketProvider(rt *runtime.Runtime) (providers.BucketProvider, error) {
	schema, err := providers.CredentialSchemaForBucket(rt.Cfg.Providers.Storage)
	if err != nil {
		return nil, err
	}
	// Map back from rt.Backend → the creds shape the factory wants.
	// Only fields the provider's CredentialSchema declares are passed;
	// extras are ignored.
	creds := map[string]string{}
	for _, f := range schema.Fields {
		// The state.Configure round-trip ran the same schema-driven
		// resolution at the cmd/cli boundary; we re-key our resolved
		// values back into the field map here. AccessKey / SecretKey
		// land via the bucket-creds envvars; other fields (like the
		// cloudflare account_id) come from rt.Providers.
		switch f.Key {
		case "access_key", "AWS_ACCESS_KEY_ID":
			creds[f.Key] = rt.Backend.AccessKey
		case "secret_key", "AWS_SECRET_ACCESS_KEY":
			creds[f.Key] = rt.Backend.SecretKey
		case "api_key":
			if rt.Providers.Cloudflare != nil {
				creds[f.Key] = rt.Providers.Cloudflare.APIKey
			}
		case "account_id":
			if rt.Providers.Cloudflare != nil {
				creds[f.Key] = rt.Providers.Cloudflare.AccountID
			}
		}
	}
	return providers.ResolveBucket(rt.Cfg.Providers.Storage, creds)
}
