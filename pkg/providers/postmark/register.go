package postmark

import "github.com/getnvoi/core/pkg/providers"

func init() {
	providers.RegisterEmail("postmark", Schema, New)
}
