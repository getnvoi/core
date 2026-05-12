package os

import "github.com/getnvoi/core/pkg/store/keyring"

func init() { keyring.Register("os", New) }
