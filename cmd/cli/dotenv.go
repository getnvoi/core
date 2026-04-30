package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// loadDotEnv reads a .env file and sets each KEY=VALUE into the
// process environment. Shell-set vars win — pre-existing values are
// not overwritten (standard dotenv semantics).
//
// Returns nil silently when the file doesn't exist; that's the common
// case (.env is gitignored, may be absent in CI).
//
// Format supported (deliberately minimal, no shell expansion):
//   - KEY=value
//   - KEY=value with spaces
//   - KEY="quoted"  /  KEY='quoted'
//   - lines starting with `#` are comments
//   - blank lines ignored
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return fmt.Errorf("%s:%d: missing `=` separator", path, lineNo)
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		if k == "" {
			return fmt.Errorf("%s:%d: empty key", path, lineNo)
		}
		// Strip surrounding matched quotes.
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[0] == v[len(v)-1] {
			v = v[1 : len(v)-1]
		}
		// Shell wins.
		if _, set := os.LookupEnv(k); set {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("%s:%d: setenv %s: %w", path, lineNo, k, err)
		}
	}
	return sc.Err()
}

// resolveDotEnv tries `.env` in cwd first, then alongside the YAML
// config. Returns the first one that exists, or "" if neither does.
// Search is deliberately bounded — no upward tree walk.
func resolveDotEnv(configPath string) string {
	if cwd, err := os.Getwd(); err == nil {
		p := filepath.Join(cwd, ".env")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if abs, err := filepath.Abs(configPath); err == nil {
		p := filepath.Join(filepath.Dir(abs), ".env")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
