package runner

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/go-version"
	install "github.com/hashicorp/hc-install"
	"github.com/hashicorp/hc-install/fs"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"
	"github.com/hashicorp/hc-install/src"
)

// pinnedTerraformVersion is the exact terraform we install. Pinned for
// reproducibility — every operator runs the same binary regardless of
// what's on PATH. Bump deliberately, not opportunistically.
const pinnedTerraformVersion = "1.9.5"

// EnsureTerraform returns a path to a working terraform binary. Searches
// the cacheDir first; downloads if absent. Idempotent across runs.
// cacheDir is resolved at the cmd/ boundary — runner doesn't read $HOME.
func EnsureTerraform(ctx context.Context, cacheDir string) (string, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", cacheDir, err)
	}

	pinned := version.Must(version.NewVersion(pinnedTerraformVersion))

	i := install.NewInstaller()
	return i.Ensure(ctx, []src.Source{
		// Use cached binary when present.
		&fs.ExactVersion{
			Product:    product.Terraform,
			Version:    pinned,
			ExtraPaths: []string{cacheDir},
		},
		// Otherwise download into cache.
		&releases.ExactVersion{
			Product:    product.Terraform,
			Version:    pinned,
			InstallDir: cacheDir,
		},
	})
}
