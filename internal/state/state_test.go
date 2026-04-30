package state_test

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/getnvoi/core/internal/config"
	"github.com/getnvoi/core/internal/log"
	"github.com/getnvoi/core/internal/providers"
	"github.com/getnvoi/core/internal/state"
)

// fakeBucket is a minimal in-memory BucketProvider — records every
// EnsureBucket call and returns canned credentials.
type fakeBucket struct {
	ensured     []string
	creds       providers.BucketCredentials
	validateErr error
	ensureErr   error
}

func (f *fakeBucket) ValidateCredentials(_ context.Context) error { return f.validateErr }
func (f *fakeBucket) EnsureBucket(_ context.Context, name string) error {
	f.ensured = append(f.ensured, name)
	return f.ensureErr
}
func (f *fakeBucket) DeleteBucket(_ context.Context, _ string) error { return nil }
func (f *fakeBucket) Credentials(_ context.Context) (providers.BucketCredentials, error) {
	return f.creds, nil
}

func silentLog() log.Log { return log.NewWith(false, io.Discard) }

func TestConfigure_EnsuresBucketAndReturnsBackend(t *testing.T) {
	bp := &fakeBucket{
		creds: providers.BucketCredentials{
			Endpoint:        "https://acct.r2.example.com",
			AccessKeyID:     "AKIATEST",
			SecretAccessKey: "secret",
			Region:          "auto",
		},
	}

	be, err := state.Configure(context.Background(), &config.Config{App: "hello", Env: "dev"}, bp, silentLog())
	if err != nil {
		t.Fatalf("Configure: %v", err)
	}

	// Bucket name follows naming convention
	if len(bp.ensured) != 1 || bp.ensured[0] != "nvoi-hello-dev-tfstate" {
		t.Errorf("EnsureBucket called with %v, want [nvoi-hello-dev-tfstate]", bp.ensured)
	}
	// Backend carries resolved creds verbatim
	if be.Bucket != "nvoi-hello-dev-tfstate" {
		t.Errorf("Bucket: got %q want nvoi-hello-dev-tfstate", be.Bucket)
	}
	if be.Endpoint != "https://acct.r2.example.com" {
		t.Errorf("Endpoint: got %q", be.Endpoint)
	}
	if be.AccessKey != "AKIATEST" || be.SecretKey != "secret" || be.Region != "auto" {
		t.Errorf("creds passthrough wrong: %+v", be)
	}
}

func TestConfigure_FailsOnInvalidCreds(t *testing.T) {
	bp := &fakeBucket{validateErr: errors.New("bad token")}
	_, err := state.Configure(context.Background(), &config.Config{App: "h", Env: "d"}, bp, silentLog())
	if err == nil {
		t.Fatal("expected error on invalid creds, got nil")
	}
	// Critical: must NOT have called EnsureBucket if creds failed validation
	if len(bp.ensured) != 0 {
		t.Errorf("EnsureBucket should not be called on invalid creds: %v", bp.ensured)
	}
}

func TestConfigure_FailsOnBucketCreateError(t *testing.T) {
	bp := &fakeBucket{ensureErr: errors.New("provider 500")}
	_, err := state.Configure(context.Background(), &config.Config{App: "h", Env: "d"}, bp, silentLog())
	if err == nil {
		t.Fatal("expected error on bucket failure, got nil")
	}
}
