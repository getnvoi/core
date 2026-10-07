package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name string, body string) {
	t.Helper()
	dir := filepath.Join(os.Getenv("HOME"), ".nvoi")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func clean(t *testing.T) *File {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NVOI_API_ENDPOINT", "")
	t.Setenv("NVOI_TOKEN", "")
	file, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func TestNothingConfiguredSaysSo(t *testing.T) {
	file := clean(t)
	if _, _, err := file.Resolve(""); !errors.Is(err, ErrNoContext) {
		t.Fatalf("err %v", err)
	}
	if file.Endpoint("") != DefaultEndpoint {
		t.Fatalf("endpoint %q", file.Endpoint(""))
	}
}

func TestSaveRoundTrips(t *testing.T) {
	file := clean(t)
	file.Set("prod", Context{Endpoint: "https://nvoi.to", Token: "t1", Workspace: "ben"})
	file.Set("dev", Context{Endpoint: "http://localhost:3000", Token: "t2", Workspace: "ben"})
	if err := file.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".nvoi", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", info.Mode().Perm())
	}

	read, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	context, name, err := read.Resolve("dev")
	if err != nil || name != "dev" || context.Endpoint != "http://localhost:3000" {
		t.Fatalf("%v %q %v", context, name, err)
	}
	if got := read.Names(); strings.Join(got, ",") != "dev,prod" {
		t.Fatalf("names %v", got)
	}
}

// One context is used without a name; among several, none is guessed: the command names one.
func TestSeveralContextsNeedANameOneDoesNot(t *testing.T) {
	file := clean(t)
	file.Set("prod", Context{Endpoint: "https://nvoi.to", Token: "t1"})
	if _, name, err := file.Resolve(""); err != nil || name != "prod" {
		t.Fatalf("one: %q %v", name, err)
	}
	file.Set("dev", Context{Endpoint: "http://localhost:3000", Token: "t2"})
	if _, _, err := file.Resolve(""); err == nil || !strings.Contains(err.Error(), "dev, prod") ||
		!strings.Contains(err.Error(), "--context") {
		t.Fatalf("several: %v", err)
	}
	if _, name, _ := file.Resolve("dev"); name != "dev" {
		t.Fatalf("named: %q", name)
	}
}

// NVOI_TOKEN is a context of its own: it never takes the workspace of the file's context, and naming a context
// as well is refused.
func TestTheEnvironmentsTokenIsAContextOfItsOwn(t *testing.T) {
	file := clean(t)
	file.Set("prod", Context{Endpoint: "https://nvoi.to", Token: "t1", Workspace: "acme"})

	t.Setenv("NVOI_API_ENDPOINT", "http://localhost:4000/")
	t.Setenv("NVOI_TOKEN", "from-env")
	context, name, err := file.Resolve("")
	if err != nil || name != EnvironmentName || context.Endpoint != "http://localhost:4000" ||
		context.Token != "from-env" || context.Workspace != "" {
		t.Fatalf("%v %q %v", context, name, err)
	}
	if _, _, err := file.Resolve("prod"); err == nil || !strings.Contains(err.Error(), "NVOI_TOKEN") {
		t.Fatalf("both: %v", err)
	}
}

// A context's token goes to its own server only.
func TestTheEnvironmentsEndpointMustBeTheContexts(t *testing.T) {
	file := clean(t)
	file.Set("prod", Context{Endpoint: "https://nvoi.to", Token: "t1"})

	t.Setenv("NVOI_API_ENDPOINT", "http://localhost:4000")
	if _, _, err := file.Resolve(""); err == nil || !strings.Contains(err.Error(), "NVOI_API_ENDPOINT") {
		t.Fatalf("err %v", err)
	}
	if file.Endpoint("") != "http://localhost:4000" {
		t.Fatalf("login goes to %q", file.Endpoint(""))
	}
	t.Setenv("NVOI_API_ENDPOINT", "https://nvoi.to/")
	if context, _, err := file.Resolve(""); err != nil || context.Token != "t1" {
		t.Fatalf("%v %v", context, err)
	}
}

// A token in the environment and no context at all: what CI has.
func TestTheEnvironmentAloneIsEnough(t *testing.T) {
	file := clean(t)
	t.Setenv("NVOI_API_ENDPOINT", "http://localhost:4000")
	t.Setenv("NVOI_TOKEN", "from-env")
	context, name, err := file.Resolve("")
	if err != nil || name != EnvironmentName || context.Token != "from-env" ||
		context.Endpoint != "http://localhost:4000" {
		t.Fatalf("%v %q %v", context, name, err)
	}
}

func TestAnUnknownContextNamesTheOnesThereAre(t *testing.T) {
	file := clean(t)
	file.Set("prod", Context{Endpoint: "https://nvoi.to", Token: "t1"})

	_, _, err := file.Resolve("nope")
	if err == nil || !strings.Contains(err.Error(), "have prod") {
		t.Fatalf("err %v", err)
	}
	if err := file.Remove("nope"); err == nil {
		t.Fatal("removed an unknown context")
	}
}

func TestRemove(t *testing.T) {
	file := clean(t)
	file.Set("prod", Context{Endpoint: "https://nvoi.to", Token: "t1"})
	if err := file.Remove("prod"); err != nil {
		t.Fatal(err)
	}
	if len(file.Contexts) != 0 {
		t.Fatalf("%v", file.Contexts)
	}
}

// The file an older build wrote holds one token and no endpoint.
func TestTheCredentialsFileBecomesTheDefaultContext(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NVOI_API_ENDPOINT", "")
	t.Setenv("NVOI_TOKEN", "")
	write(t, "credentials.json", `{"token":"old-jwt"}`)

	file, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	context, name, err := file.Resolve("")
	if err != nil || name != "default" || context.Token != "old-jwt" {
		t.Fatalf("%v %q %v", context, name, err)
	}
	if context.Endpoint != DefaultEndpoint {
		t.Fatalf("endpoint %q", context.Endpoint)
	}
	// Written out, so the next run reads the contexts rather than migrating again.
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".nvoi", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved File
	if err := json.Unmarshal(data, &saved); err != nil || saved.Contexts["default"].Token != "old-jwt" {
		t.Fatalf("%s %v", data, err)
	}
}

func TestABrokenFileNamesItself(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	write(t, "config.json", "{not json")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("err %v", err)
	}
}
