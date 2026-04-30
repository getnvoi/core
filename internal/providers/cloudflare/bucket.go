package cloudflare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/getnvoi/core/internal/providers"
	"github.com/getnvoi/core/internal/utils"
)

// BucketClient manages R2 buckets via Cloudflare API + returns
// S3-compatible credentials suitable for terraform's s3 backend.
type BucketClient struct {
	api       *utils.HTTPClient
	apiKey    string
	accountID string
	creds     *providers.BucketCredentials
}

// NewBucket constructs the R2 BucketProvider from resolved creds.
func NewBucket(creds map[string]string) *BucketClient {
	apiKey := creds["api_key"]
	return &BucketClient{
		api:       newAPI(apiKey, "cloudflare r2"),
		apiKey:    apiKey,
		accountID: creds["account_id"],
	}
}

func (c *BucketClient) ValidateCredentials(ctx context.Context) error {
	if c.apiKey == "" {
		return fmt.Errorf("cloudflare r2: api_key required")
	}
	if c.accountID == "" {
		return fmt.Errorf("cloudflare r2: account_id required")
	}
	if _, err := c.tokenVerify(ctx); err != nil {
		return fmt.Errorf("cloudflare r2: %w", err)
	}
	return nil
}

func (c *BucketClient) EnsureBucket(ctx context.Context, name string) error {
	err := c.api.Do(ctx, "POST", fmt.Sprintf("/accounts/%s/r2/buckets", c.accountID),
		map[string]string{"name": name}, nil,
	)
	if err == nil {
		return nil
	}
	// 409 = already exists — success (idempotent).
	var apiErr *utils.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPStatus() == 409 {
		return nil
	}
	return fmt.Errorf("create bucket %s: %w", name, err)
}

func (c *BucketClient) DeleteBucket(ctx context.Context, name string) error {
	err := c.api.Do(ctx, "DELETE", fmt.Sprintf("/accounts/%s/r2/buckets/%s", c.accountID, name), nil, nil)
	if err != nil {
		if utils.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("delete bucket %s: %w", name, err)
	}
	return nil
}

// Credentials returns S3-compatible access details for the R2 bucket
// service. Uses the operator's CF API token: AccessKeyID is the
// token's UUID, SecretAccessKey is sha256(token) hex-encoded — the
// derivation pattern Cloudflare documents for token-based S3 auth.
func (c *BucketClient) Credentials(ctx context.Context) (providers.BucketCredentials, error) {
	if c.creds != nil {
		return *c.creds, nil
	}
	tokenID, err := c.tokenVerify(ctx)
	if err != nil {
		return providers.BucketCredentials{}, fmt.Errorf("cloudflare credentials: %w", err)
	}
	hash := sha256.Sum256([]byte(c.apiKey))
	c.creds = &providers.BucketCredentials{
		Endpoint:        fmt.Sprintf("https://%s.r2.cloudflarestorage.com", c.accountID),
		AccessKeyID:     tokenID,
		SecretAccessKey: hex.EncodeToString(hash[:]),
		Region:          "auto",
	}
	return *c.creds, nil
}

func (c *BucketClient) tokenVerify(ctx context.Context) (string, error) {
	var result struct {
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := c.api.Do(ctx, "GET", "/user/tokens/verify", nil, &result); err != nil {
		return "", err
	}
	if result.Result.ID == "" {
		return "", fmt.Errorf("invalid API token")
	}
	return result.Result.ID, nil
}

var _ providers.BucketProvider = (*BucketClient)(nil)
