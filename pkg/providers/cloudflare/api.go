// Package cloudflare implements the BucketProvider for Cloudflare R2.
// Bucket lifecycle (create/delete) goes through the CF REST API.
// Credentials returns S3-compatible access details for terraform's s3
// backend.
package cloudflare

import (
	"net/http"
)

const baseURL = "https://api.cloudflare.com/client/v4"

func newAPI(apiKey, label string) *HTTPClient {
	return &HTTPClient{
		BaseURL: baseURL,
		SetAuth: func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+apiKey)
		},
		Label: label,
	}
}
