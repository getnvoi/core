package file

import "github.com/getnvoi/core/pkg/store/keyring"

func init() { keyring.Register("file", New) }
