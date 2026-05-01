package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func LoadDotEnv(path string) error {
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
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[0] == v[len(v)-1] {
			v = v[1 : len(v)-1]
		}
		if _, set := os.LookupEnv(k); set {
			continue
		}
		if err := os.Setenv(k, v); err != nil {
			return fmt.Errorf("%s:%d: setenv %s: %w", path, lineNo, k, err)
		}
	}
	return sc.Err()
}

func ResolveDotEnv(configPath string) string {
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
