// Package s3 provides S3-compatible operations with AWS Signature V4
// signing. Pure — no config, no env vars, no side effects.
//
// Used by the database backup substrate (Put / PutStream / GetStream /
// ListObjects) and by cmd/db (the unified backup/restore container
// image). Public-surface package because cmd/db lives at cmd/ —
// outside the pkg/internal/ reach.
package s3

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// s3Client is the HTTP client used for S3 operations. 30s timeout,
// consistent with the rest of the codebase. Not utils.HTTPClient
// because S3/XML operations need raw HTTP, not JSON.
var s3Client = &http.Client{Timeout: 30 * time.Second}

// Put uploads data to an S3-compatible endpoint.
func Put(endpoint, accessKey, secretKey, bucket, key string, body []byte) error {
	url := fmt.Sprintf("%s/%s/%s", endpoint, bucket, key)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("s3 put request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	Sign(req, body, accessKey, secretKey, "auto")

	resp, err := s3Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 put %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// PutStream uploads a stream of known size to an S3-compatible endpoint
// using UNSIGNED-PAYLOAD — avoids buffering the whole body to compute a
// sha256. Required for large database dumps: Put() loads everything
// into memory, which OOMs pods at ≥500 MB.
//
// AWS S3, Cloudflare R2, and Scaleway Object Storage all accept
// UNSIGNED-PAYLOAD for PutObject. The server still validates
// Content-Length, so the caller MUST pass the exact byte count.
//
// Used by cmd/db, which streams `pg_dump | gzip` from a temp file
// after it's fully written (stat → size → upload). Lives next to Put()
// because both share Sign() / the same endpoint/key URL layout.
func PutStream(endpoint, accessKey, secretKey, bucket, key string, body io.Reader, size int64, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	url := fmt.Sprintf("%s/%s/%s", endpoint, bucket, key)
	req, err := http.NewRequest("PUT", url, body)
	if err != nil {
		return fmt.Errorf("s3 put-stream request: %w", err)
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	SignUnsigned(req, accessKey, secretKey, "auto")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 put-stream %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// Get downloads an object from an S3-compatible endpoint. Returns the
// body bytes and content type. Returns error for non-2xx responses.
func Get(endpoint, accessKey, secretKey, bucket, key string) ([]byte, string, error) {
	url := fmt.Sprintf("%s/%s/%s", endpoint, bucket, key)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("s3 get request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	Sign(req, nil, accessKey, secretKey, "auto")

	resp, err := s3Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("s3 get %d: %s/%s", resp.StatusCode, bucket, key)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// GetStream opens a streaming connection to an S3 object. Returns the
// response body (caller must close), content type, and content length.
// Use this instead of Get for large objects to avoid loading them
// into memory.
func GetStream(endpoint, accessKey, secretKey, bucket, key string) (io.ReadCloser, string, int64, error) {
	url := fmt.Sprintf("%s/%s/%s", endpoint, bucket, key)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", 0, fmt.Errorf("s3 get request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	Sign(req, nil, accessKey, secretKey, "auto")

	resp, err := s3Client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}

	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, "", 0, fmt.Errorf("s3 get %d: %s/%s", resp.StatusCode, bucket, key)
	}
	return resp.Body, resp.Header.Get("Content-Type"), resp.ContentLength, nil
}

// Object represents an item in an S3 bucket listing.
type Object struct {
	Key          string
	Size         int64
	LastModified string
}

// ListObjects lists objects in an S3-compatible bucket. Returns all
// objects (handles pagination via IsTruncated).
func ListObjects(endpoint, accessKey, secretKey, bucket, prefix string) ([]Object, error) {
	var all []Object
	continuation := ""
	for {
		url := fmt.Sprintf("%s/%s?list-type=2&max-keys=1000", endpoint, bucket)
		if prefix != "" {
			url += "&prefix=" + prefix
		}
		if continuation != "" {
			url += "&continuation-token=" + continuation
		}
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/xml")
		Sign(req, nil, accessKey, secretKey, "auto")

		resp, err := s3Client.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("s3 list %s: read body: %w", bucket, err)
		}
		if resp.StatusCode >= 300 {
			return nil, fmt.Errorf("s3 list %s: %d: %s", bucket, resp.StatusCode, strings.TrimSpace(string(body)))
		}

		var result listBucketResult
		if err := parseXML(body, &result); err != nil {
			return nil, fmt.Errorf("s3 list %s: parse: %w", bucket, err)
		}
		for _, c := range result.Contents {
			all = append(all, Object{Key: c.Key, Size: c.Size, LastModified: c.LastModified})
		}
		if !result.IsTruncated {
			break
		}
		continuation = result.NextContinuationToken
	}
	return all, nil
}

type listBucketResult struct {
	Contents []struct {
		Key          string `xml:"Key"`
		Size         int64  `xml:"Size"`
		LastModified string `xml:"LastModified"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

func parseXML(data []byte, v any) error {
	return xml.Unmarshal(data, v)
}

// SignUnsigned signs a request whose body's sha256 we don't want to
// compute. Sets `x-amz-content-sha256: UNSIGNED-PAYLOAD` — supported by
// AWS S3, R2, and Scaleway for PutObject requests. Used by PutStream()
// so large objects (database dumps) stream from disk without a
// full-body hash pass.
//
// The rest of the SigV4 canonicalization matches Sign() exactly, which
// is why both are in this file.
func SignUnsigned(req *http.Request, accessKey, secretKey, region string) {
	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	const payloadHash = "UNSIGNED-PAYLOAD"
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("Host", req.URL.Host)

	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	signedHeaders := "content-type;host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		req.Header.Get("Content-Type"), req.URL.Host, payloadHash, amzDate)
	canonicalRequest := strings.Join([]string{
		req.Method, canonicalURI, req.URL.Query().Encode(), canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", datestamp, region)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, sha256Hex([]byte(canonicalRequest)))

	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature))
}

// Sign adds AWS Signature V4 headers to an HTTP request. region is the
// S3 region ("auto" for R2, "us-east-1" for AWS, etc.)
func Sign(req *http.Request, body []byte, accessKey, secretKey, region string) {
	now := time.Now().UTC()
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	payloadHash := sha256Hex(body)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("Host", req.URL.Host)

	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	signedHeaders := "content-type;host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("content-type:%s\nhost:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		req.Header.Get("Content-Type"), req.URL.Host, payloadHash, amzDate)
	canonicalRequest := strings.Join([]string{
		req.Method, canonicalURI, req.URL.Query().Encode(), canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", datestamp, region)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, credentialScope, sha256Hex([]byte(canonicalRequest)))

	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature))
}

// SetLifecycle puts an expiration rule on a bucket. days is the
// retention in days — every object older than that gets deleted by
// the storage provider's lifecycle processor (typically once per
// day, eventually consistent).
//
// Used by the database backup substrate: backup.retention: <days>
// in YAML maps directly to this call against the per-DB backup
// bucket. AWS S3, Cloudflare R2, and Scaleway Object Storage all
// honor the PutBucketLifecycleConfiguration shape.
func SetLifecycle(endpoint, accessKey, secretKey, region, bucket string, days int) error {
	body := []byte(fmt.Sprintf(`<LifecycleConfiguration>
  <Rule>
    <ID>nvoi-expire</ID>
    <Status>Enabled</Status>
    <Expiration><Days>%d</Days></Expiration>
  </Rule>
</LifecycleConfiguration>`, days))

	url := fmt.Sprintf("%s/%s?lifecycle", strings.TrimRight(endpoint, "/"), bucket)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/xml")
	if region == "" {
		region = "auto"
	}
	Sign(req, body, accessKey, secretKey, region)

	resp, err := s3Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 set lifecycle %s: %d: %s", bucket, resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return nil
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}
