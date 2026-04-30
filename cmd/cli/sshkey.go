package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readSSHKeys resolves the operator's SSH keypair from the public-key
// path declared in YAML. Convention: cfg.SSHKey points at `*.pub`; the
// matching private key is the same path with `.pub` stripped (e.g.
// ~/.ssh/id_rsa.pub → ~/.ssh/id_rsa). Same convention as ssh-keygen
// itself produces and 99% of operator setups use.
//
// Both keys live at the cmd/ boundary; internal packages get bytes,
// not paths.
func readSSHKeys(pubPath, home string) (pub, priv []byte, err error) {
	pubPath = expandHome(pubPath, home)
	pub, err = readSSHFile(pubPath, "public")
	if err != nil {
		return nil, nil, err
	}

	privPath := strings.TrimSuffix(pubPath, ".pub")
	if privPath == pubPath {
		return nil, nil, fmt.Errorf("ssh_key %s: expected a .pub path so the matching private key can be derived", pubPath)
	}
	priv, err = readSSHFile(privPath, "private")
	if err != nil {
		return nil, nil, err
	}
	return pub, priv, nil
}

func expandHome(p, home string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}

func readSSHFile(path, kind string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s ssh key %s: %w", kind, path, err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, fmt.Errorf("%s ssh key %s: empty file", kind, path)
	}
	if kind == "private" {
		// Preserve line breaks for PEM parsing; only reject empty.
		return raw, nil
	}
	return []byte(trimmed), nil
}
