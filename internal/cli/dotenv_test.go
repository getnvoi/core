package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// envCleanup unsets a list of vars at test end. Used because LoadDotEnv
// goes through os.Setenv (the function under test mutates real env);
// t.Setenv() can't help because we want vars created by LoadDotEnv to
// not leak across tests.
func envCleanup(t *testing.T, keys ...string) {
	t.Helper()
	t.Cleanup(func() {
		for _, k := range keys {
			_ = os.Unsetenv(k)
		}
	})
}

func writeEnvFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestLoadDotEnv_MissingFileIsNoop(t *testing.T) {
	if err := LoadDotEnv("/no/such/path/.env"); err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
}

func TestLoadDotEnv_SetsVarsFromFile(t *testing.T) {
	envCleanup(t, "TEST_NVOI_FOO", "TEST_NVOI_BAR")
	p := writeEnvFile(t, "TEST_NVOI_FOO=hello\nTEST_NVOI_BAR=world\n")
	if err := LoadDotEnv(p); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv("TEST_NVOI_FOO"); got != "hello" {
		t.Errorf("TEST_NVOI_FOO: %q", got)
	}
	if got := os.Getenv("TEST_NVOI_BAR"); got != "world" {
		t.Errorf("TEST_NVOI_BAR: %q", got)
	}
}

func TestLoadDotEnv_SkipsCommentsAndBlanks(t *testing.T) {
	envCleanup(t, "TEST_NVOI_REAL")
	p := writeEnvFile(t, "# leading comment\n\nTEST_NVOI_REAL=ok\n# trailing comment\n")
	if err := LoadDotEnv(p); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv("TEST_NVOI_REAL"); got != "ok" {
		t.Errorf("got %q", got)
	}
}

func TestLoadDotEnv_StripsQuotes(t *testing.T) {
	envCleanup(t, "TEST_NVOI_DQ", "TEST_NVOI_SQ")
	p := writeEnvFile(t, `TEST_NVOI_DQ="value with spaces"`+"\n"+`TEST_NVOI_SQ='single'`+"\n")
	if err := LoadDotEnv(p); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv("TEST_NVOI_DQ"); got != "value with spaces" {
		t.Errorf("double-quoted: %q", got)
	}
	if got := os.Getenv("TEST_NVOI_SQ"); got != "single" {
		t.Errorf("single-quoted: %q", got)
	}
}

func TestLoadDotEnv_DoesNotOverrideExistingVars(t *testing.T) {
	t.Setenv("TEST_NVOI_PRESET", "from-shell")
	p := writeEnvFile(t, "TEST_NVOI_PRESET=from-file\n")
	if err := LoadDotEnv(p); err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if got := os.Getenv("TEST_NVOI_PRESET"); got != "from-shell" {
		t.Errorf("env var should win over .env file; got %q want from-shell", got)
	}
}

func TestLoadDotEnv_RejectsLineWithoutEquals(t *testing.T) {
	p := writeEnvFile(t, "TEST_NVOI_OK=fine\nNO_EQUALS_HERE\n")
	if err := LoadDotEnv(p); err == nil {
		t.Fatal("expected parse error on line missing `=`")
	}
}

func TestLoadDotEnv_RejectsEmptyKey(t *testing.T) {
	p := writeEnvFile(t, "=value\n")
	if err := LoadDotEnv(p); err == nil {
		t.Fatal("expected error on empty key")
	}
}

func TestResolveDotEnv_PrefersCwd(t *testing.T) {
	// Both candidate locations have a .env. Cwd must win.
	// macOS canonicalizes /var → /private/var on Getwd, so we use the
	// post-chdir Getwd as the source of truth for the expected path.
	cwd := t.TempDir()
	cfgDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".env"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, ".env"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	canonicalCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalCwd, ".env")

	got := ResolveDotEnv(filepath.Join(cfgDir, "nvoi.yaml"))
	if got != want {
		t.Errorf("cwd .env should win: got %q want %q", got, want)
	}
}

func TestResolveDotEnv_FallsBackToConfigDir(t *testing.T) {
	cwd := t.TempDir() // no .env here
	cfgDir := t.TempDir()
	cfgEnv := filepath.Join(cfgDir, ".env")
	if err := os.WriteFile(cfgEnv, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	got := ResolveDotEnv(filepath.Join(cfgDir, "nvoi.yaml"))
	if got != cfgEnv {
		t.Errorf("config-dir .env should win: got %q want %q", got, cfgEnv)
	}
}

func TestResolveDotEnv_ReturnsEmptyWhenNoneFound(t *testing.T) {
	cwd := t.TempDir()
	cfgDir := t.TempDir()
	prev, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	got := ResolveDotEnv(filepath.Join(cfgDir, "nvoi.yaml"))
	if got != "" {
		t.Errorf("expected empty when no .env anywhere, got %q", got)
	}
}
