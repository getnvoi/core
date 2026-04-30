// Package hetzner emits Hetzner-Cloud HCL. Single source of truth for
// the hetzner backend; growing this package = porting more of nvoi's
// pkg/provider/hetzner over.
package hetzner

import "github.com/getnvoi/tf/internal/compile"

func init() {
	compile.RegisterInfra("hetzner", &emitter{})
}
