package runner

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// pinnedTofuVersion is the exact OpenTofu we install. Pinned for
// reproducibility — every operator runs the same binary regardless of
// what's on PATH. Bump deliberately, not opportunistically.
//
// nvoi ships OpenTofu (MPL-2.0) instead of HashiCorp Terraform (BUSL-1.1)
// to keep the deploy pipeline on a permissive licence.
const pinnedTofuVersion = "1.11.6"

// releaseBaseURL is the GitHub release CDN. Versioned download paths
// follow `v<ver>/tofu_<ver>_<os>_<arch>.zip` and a sibling
// `tofu_<ver>_SHA256SUMS` file we verify against.
const releaseBaseURL = "https://github.com/opentofu/opentofu/releases/download"

// EnsureTofu returns a path to a working tofu binary. Searches the
// versioned slot inside cacheDir first; downloads + checksums + unzips
// if absent. Idempotent across runs. cacheDir is resolved at the cmd/
// boundary — runner doesn't read $HOME.
//
// Layout: <cacheDir>/tofu-<ver>/tofu — versioned so a future bump
// lands alongside, never overwriting.
func EnsureTofu(ctx context.Context, cacheDir string) (string, error) {
	versionedDir := filepath.Join(cacheDir, "tofu-"+pinnedTofuVersion)
	binPath := filepath.Join(versionedDir, "tofu")

	if st, err := os.Stat(binPath); err == nil && !st.IsDir() {
		return binPath, nil
	}

	if err := os.MkdirAll(versionedDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", versionedDir, err)
	}

	osName, archName, err := platform()
	if err != nil {
		return "", err
	}

	zipName := fmt.Sprintf("tofu_%s_%s_%s.zip", pinnedTofuVersion, osName, archName)
	zipURL := fmt.Sprintf("%s/v%s/%s", releaseBaseURL, pinnedTofuVersion, zipName)
	sumsURL := fmt.Sprintf("%s/v%s/tofu_%s_SHA256SUMS", releaseBaseURL, pinnedTofuVersion, pinnedTofuVersion)

	zipPath := filepath.Join(versionedDir, zipName)
	if err := downloadFile(ctx, zipURL, zipPath); err != nil {
		return "", fmt.Errorf("download %s: %w", zipURL, err)
	}
	defer os.Remove(zipPath)

	wantSum, err := fetchExpectedSum(ctx, sumsURL, zipName)
	if err != nil {
		return "", fmt.Errorf("fetch checksum: %w", err)
	}
	gotSum, err := sha256File(zipPath)
	if err != nil {
		return "", fmt.Errorf("sha256 %s: %w", zipPath, err)
	}
	if gotSum != wantSum {
		return "", fmt.Errorf("checksum mismatch for %s: want %s got %s", zipName, wantSum, gotSum)
	}

	if err := extractTofuBinary(zipPath, binPath); err != nil {
		return "", fmt.Errorf("extract %s: %w", zipPath, err)
	}
	if err := os.Chmod(binPath, 0o755); err != nil {
		return "", fmt.Errorf("chmod %s: %w", binPath, err)
	}
	return binPath, nil
}

// platform maps Go's runtime identifiers to OpenTofu release asset
// suffixes. The OpenTofu release pipeline uses GOOS/GOARCH verbatim,
// so this is a passthrough plus a guard for archs we haven't published.
func platform() (string, string, error) {
	osName := runtime.GOOS
	archName := runtime.GOARCH
	switch osName {
	case "darwin", "linux", "freebsd", "openbsd", "windows":
	default:
		return "", "", fmt.Errorf("unsupported os %q for OpenTofu release", osName)
	}
	switch archName {
	case "amd64", "arm64", "386", "arm":
	default:
		return "", "", fmt.Errorf("unsupported arch %q for OpenTofu release", archName)
	}
	return osName, archName, nil
}

// downloadFile streams url → dst with a context-bound HTTP request.
// Truncates dst on success; the temp file cleanup is the caller's job
// (EnsureTofu defers os.Remove on the zip).
func downloadFile(ctx context.Context, url, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}

// fetchExpectedSum pulls the release's SHA256SUMS file and returns the
// hex digest for assetName. Format is goreleaser-standard:
// `<sha256>  <filename>` per line.
func fetchExpectedSum(ctx context.Context, url, assetName string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == assetName {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("checksum for %s not found in %s", assetName, url)
}

// sha256File hashes a file's contents and returns the hex digest.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractTofuBinary unzips the tofu binary out of the release archive
// into dst. Release zips contain `tofu` (or `tofu.exe` on windows) at
// the archive root alongside a LICENSE file — we only want the binary.
func extractTofuBinary(zipPath, dst string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	binName := "tofu"
	if runtime.GOOS == "windows" {
		binName = "tofu.exe"
	}

	for _, f := range r.File {
		if filepath.Base(f.Name) != binName {
			continue
		}
		in, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.Create(dst)
		if err != nil {
			in.Close()
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			out.Close()
			return err
		}
		in.Close()
		if err := out.Close(); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("%s not found in archive %s", binName, zipPath)
}
