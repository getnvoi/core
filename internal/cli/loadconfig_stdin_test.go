package cli

import (
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/config"

	// blank-imported so the hetzner emitter registers reserved server
	// names before Validate runs against a hetzner config below.
	_ "github.com/getnvoi/core/pkg/providers/hetzner"
)

// withStdin replaces os.Stdin for the duration of fn with a pipe
// pre-filled with `data`. The original is restored on cleanup.
func withStdin(t *testing.T, data string, fn func()) {
	t.Helper()
	orig := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := io.WriteString(w, data); err != nil {
		t.Fatalf("stdin write: %v", err)
	}
	w.Close()
	os.Stdin = r
	defer func() { os.Stdin = orig; r.Close() }()
	fn()
}

func minimalYAML() string {
	return `app: stdin-test
env: ci
providers:
  infra: hetzner
ssh_key: ~/.ssh/id_rsa.pub
servers:
  master: {type: cax11, region: nbg1, role: master}
`
}

// TestLoadConfigFromStdin — feeding yaml on stdin via the "-" path
// produces the same *config.Config a real file would.
func TestLoadConfigFromStdin(t *testing.T) {
	withStdin(t, minimalYAML(), func() {
		got, err := LoadConfig(StdinConfigPath)
		if err != nil {
			t.Fatalf("LoadConfig(-): %v", err)
		}
		want, err := config.ParseYAML([]byte(minimalYAML()))
		if err != nil {
			t.Fatalf("ParseYAML reference: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("stdin vs in-memory parse differ:\n got %+v\nwant %+v", got, want)
		}
	})
}

// TestLoadConfigFromStdinInvalid — invalid yaml on stdin surfaces the
// same validation error a bad file would.
func TestLoadConfigFromStdinInvalid(t *testing.T) {
	withStdin(t, `app: x`, func() { // missing env, providers, ssh_key, servers
		_, err := LoadConfig(StdinConfigPath)
		if err == nil {
			t.Fatal("incomplete yaml from stdin: want validation error")
		}
		if !strings.Contains(err.Error(), "env") {
			t.Fatalf("error should mention missing env: got %q", err.Error())
		}
	})
}

// TestLoadConfigFromStdinEmpty — empty stdin should error cleanly, not
// produce a zero-value Config that passes validation.
func TestLoadConfigFromStdinEmpty(t *testing.T) {
	withStdin(t, "", func() {
		_, err := LoadConfig(StdinConfigPath)
		if err == nil {
			t.Fatal("empty stdin: want error (validation should fail on missing app)")
		}
	})
}

// TestResolveDotEnvSkipsForStdin — stdin mode must NOT auto-load a
// .env from cwd. The host owns env in this mode; surprise file reads
// would be a footgun.
func TestResolveDotEnvSkipsForStdin(t *testing.T) {
	// Create a tmp cwd with a .env so the alongside-cwd branch WOULD
	// otherwise return it.
	tmp := t.TempDir()
	envPath := tmp + "/.env"
	if err := os.WriteFile(envPath, []byte("X=y\n"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	prevWD, _ := os.Getwd()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(prevWD)

	// Real file path → finds the .env from cwd (macOS may symlink
	// /var → /private/var, so compare via suffix rather than literal).
	if got := ResolveDotEnv("nvoi.yaml"); !strings.HasSuffix(got, "/.env") || got == "" {
		t.Fatalf("file mode: got %q, want suffix /.env", got)
	}
	// Stdin sentinel → empty, no auto-load
	if got := ResolveDotEnv(StdinConfigPath); got != "" {
		t.Fatalf("stdin mode: got %q want \"\"", got)
	}
}
