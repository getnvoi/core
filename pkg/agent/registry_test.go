package agent

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// stubAgent is a minimal Agent for registry tests — its Loop never
// runs (registry tests don't invoke it). A non-nil identity is enough
// to verify the factory wired correctly.
type stubAgent struct{ name string }

func (s *stubAgent) Loop(ctx context.Context, spec LaunchSpec, token string, out io.Writer) error {
	return errors.New("stubAgent.Loop should not be called in registry tests")
}

// resetRegistry empties the package-level registry between tests. The
// tests register stub names then must clean up so subsequent tests
// don't see leftover entries (the registry is shared package state).
func resetRegistry(t *testing.T) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	for k := range registry {
		delete(registry, k)
	}
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for k := range registry {
			delete(registry, k)
		}
	})
}

func TestRegisterAndResolve(t *testing.T) {
	resetRegistry(t)
	Register("alpha", func() Agent { return &stubAgent{name: "alpha"} })

	a, err := Resolve("alpha")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	sa, ok := a.(*stubAgent)
	if !ok {
		t.Fatalf("Resolve returned %T, want *stubAgent", a)
	}
	if sa.name != "alpha" {
		t.Fatalf("identity: got %q want alpha", sa.name)
	}
}

func TestResolveUnknownErrorMentionsRegistered(t *testing.T) {
	resetRegistry(t)
	Register("alpha", func() Agent { return &stubAgent{} })
	Register("beta", func() Agent { return &stubAgent{} })

	_, err := Resolve("gamma")
	if err == nil {
		t.Fatal("unknown name: want error")
	}
	if !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Fatalf("error should list registered names, got %q", err.Error())
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	resetRegistry(t)
	Register("dup", func() Agent { return &stubAgent{} })

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("duplicate Register: want panic")
		}
		if !strings.Contains(r.(string), "duplicate") {
			t.Fatalf("panic msg: got %q", r)
		}
	}()
	Register("dup", func() Agent { return &stubAgent{} })
}

func TestRegisterEmptyNamePanics(t *testing.T) {
	resetRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("empty name: want panic")
		}
	}()
	Register("", func() Agent { return &stubAgent{} })
}

func TestRegisterNilFactoryPanics(t *testing.T) {
	resetRegistry(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("nil factory: want panic")
		}
	}()
	Register("x", nil)
}

func TestNamesSorted(t *testing.T) {
	resetRegistry(t)
	for _, n := range []string{"zeta", "alpha", "mu"} {
		Register(n, func() Agent { return &stubAgent{} })
	}
	got := Names()
	want := []string{"alpha", "mu", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Names: got %v want %v", got, want)
	}
}

func TestResolveCallsFactoryPerCall(t *testing.T) {
	// Each Resolve should invoke the factory fresh — agents may hold
	// per-turn state and must not be shared across invocations.
	resetRegistry(t)
	var built int32
	var mu sync.Mutex
	Register("counted", func() Agent {
		mu.Lock()
		built++
		mu.Unlock()
		return &stubAgent{}
	})
	for i := 0; i < 3; i++ {
		if _, err := Resolve("counted"); err != nil {
			t.Fatalf("Resolve iter %d: %v", i, err)
		}
	}
	if built != 3 {
		t.Fatalf("factory called %d times, want 3", built)
	}
}
