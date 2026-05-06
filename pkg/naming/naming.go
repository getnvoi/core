// Package naming centralizes every deterministic name nvoi produces.
// Same env → same names → same resources, no UUIDs. Pure string
// assembly: no env reads, no disk, no network.
package naming

import (
	"fmt"
	"path/filepath"
)

// DefaultUser is the unprivileged login every cloud-init renders.
// Matches `../nvoi/pkg/utils.DefaultUser` so SSH dispatch keeps working
// as we port more steps.
const DefaultUser = "deploy"

// CacheDirSegment is the leaf path under the operator's cache root
// (`~/.cache`) where the embedded tofu binary lands. Composed at
// the cmd/ boundary — naming never reads $HOME.
const CacheDirSegment = "nvoi"

// Prefix returns the cluster-scoped prefix shared by every resource:
// `nvoi-{app}-{env}`.
func Prefix(app, env string) string { return fmt.Sprintf("nvoi-%s-%s", app, env) }

// Server returns the provider-side server name for the given short name.
func Server(app, env, name string) string { return Prefix(app, env) + "-" + name }

// StateBucket returns the object-storage bucket holding TF remote state.
// Unused while the state backend is local; preserved as the seam to
// re-promote remote state.
func StateBucket(app, env string) string { return Prefix(app, env) + "-tfstate" }

// WorkDir returns the per-(app, env) tofu working directory under
// the operator's cwd: `.tf/{app}-{env}/`. Bundle files + `terraform.tfstate`
// (filename retained by OpenTofu for state-format compat) land here.
func WorkDir(app, env string) string { return filepath.Join(workDirRoot, app+"-"+env) }

// ProviderHCL returns the .tf filename a provider's emitter writes into
// the bundle: `<provider>.tf`. One file per registered provider.
func ProviderHCL(providerName string) string { return providerName + ".tf" }

const workDirRoot = ".tf"
