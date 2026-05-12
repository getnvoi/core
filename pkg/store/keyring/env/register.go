package env

import "github.com/getnvoi/core/pkg/store/keyring"

func init() { keyring.Register("env", New) }
