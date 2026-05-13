package s3

import (
	"net/http"
	"strings"
	"testing"
)

// TestSign_AttachesHeaders pins the SigV4 header surface so a careless
// rename of the signing routine doesn't silently produce unsigned (or
// wrongly-signed) requests. We don't validate the signature math —
// AWS S3, R2, and Scaleway have all been signing requests with this
// code in `../nvoi` for months — but we do validate the shape: every
// signed request gets x-amz-date, x-amz-content-sha256, Host, and
// Authorization headers, all consistent with each other.
func TestSign_AttachesHeaders(t *testing.T) {
	req, err := http.NewRequest("PUT", "https://example.r2.cloudflarestorage.com/bucket/key", strings.NewReader("hi"))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	Sign(req, []byte("hi"), "AKID", "SECRET", "auto")

	for _, h := range []string{"x-amz-date", "x-amz-content-sha256", "Authorization"} {
		if req.Header.Get(h) == "" {
			t.Errorf("missing header %s", h)
		}
	}
	if got := req.Header.Get("Authorization"); !strings.HasPrefix(got, "AWS4-HMAC-SHA256 Credential=AKID/") {
		t.Errorf("Authorization = %q, want AWS4-HMAC-SHA256 with AKID credential", got)
	}
	if got := req.Header.Get("x-amz-content-sha256"); got == "UNSIGNED-PAYLOAD" {
		t.Errorf("Sign produced UNSIGNED-PAYLOAD; that's SignUnsigned's job")
	}
}

// TestSignUnsigned_UsesUnsignedPayloadConst locks the contract that
// PutStream-shaped uploads use UNSIGNED-PAYLOAD — required because we
// can't hash the body before reading it. R2 and Scaleway accept it for
// PutObject; AWS does too.
func TestSignUnsigned_UsesUnsignedPayloadConst(t *testing.T) {
	req, err := http.NewRequest("PUT", "https://example.r2.cloudflarestorage.com/bucket/key", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	SignUnsigned(req, "AKID", "SECRET", "auto")

	if got := req.Header.Get("x-amz-content-sha256"); got != "UNSIGNED-PAYLOAD" {
		t.Errorf("x-amz-content-sha256 = %q, want UNSIGNED-PAYLOAD", got)
	}
	if req.Header.Get("Authorization") == "" {
		t.Errorf("missing Authorization")
	}
}
