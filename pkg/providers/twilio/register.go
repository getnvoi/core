package twilio

import "github.com/getnvoi/core/pkg/providers"

func init() {
	providers.RegisterSMS("twilio", Schema, New)
}
