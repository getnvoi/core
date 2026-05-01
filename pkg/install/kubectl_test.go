package install_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/getnvoi/core/pkg/install"
	"github.com/getnvoi/core/pkg/testutil/sshfake"
)

func TestKubectlExec_AssemblesQuotedCommand(t *testing.T) {
	sh := &sshfake.Shell{} // no matchers — RunStream succeeds with empty stdout

	err := install.KubectlExec(context.Background(), "statefulset/postgres", install.KubectlSpec{
		Shell:  sh,
		Args:   []string{"psql", "-U", "nvoi", "-d", "nvoi", "-tAc", "SELECT COUNT(*) FROM visits"},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("KubectlExec: %v", err)
	}
	if len(sh.Calls) != 1 {
		t.Fatalf("expected 1 ssh call, got %d: %v", len(sh.Calls), sh.Calls)
	}
	cmd := sh.Calls[0]
	// Target stays unquoted (DNS-1123 names are shell-safe by spec).
	if !strings.Contains(cmd, "sudo k3s kubectl exec statefulset/postgres -- ") {
		t.Errorf("target form wrong: %q", cmd)
	}
	// Each user arg is single-quoted, including the SQL with parens
	// and stars that bash would otherwise glob.
	for _, want := range []string{
		"'psql'", "'-U'", "'nvoi'", "'-d'", "'nvoi'", "'-tAc'",
		"'SELECT COUNT(*) FROM visits'",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("expected %q in command, got: %q", want, cmd)
		}
	}
}

func TestKubectlExec_RejectsEmptyTarget(t *testing.T) {
	sh := &sshfake.Shell{}
	err := install.KubectlExec(context.Background(), "", install.KubectlSpec{
		Shell: sh, Args: []string{"ls"}, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err == nil {
		t.Error("expected error for empty target")
	}
	if len(sh.Calls) != 0 {
		t.Errorf("nothing should have been sent over ssh: %v", sh.Calls)
	}
}

func TestKubectlExec_RejectsEmptyArgs(t *testing.T) {
	sh := &sshfake.Shell{}
	err := install.KubectlExec(context.Background(), "deploy/web", install.KubectlSpec{
		Shell: sh, Args: nil, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err == nil {
		t.Error("expected error for empty args")
	}
	if len(sh.Calls) != 0 {
		t.Errorf("nothing should have been sent over ssh: %v", sh.Calls)
	}
}

func TestKubectlExec_PreservesSingleQuotesInsideArg(t *testing.T) {
	sh := &sshfake.Shell{}
	err := install.KubectlExec(context.Background(), "deploy/web", install.KubectlSpec{
		Shell:  sh,
		Args:   []string{"sh", "-c", `echo "it's fine"`},
		Stdout: io.Discard,
		Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("KubectlExec: %v", err)
	}
	cmd := sh.Calls[0]
	// `it's` inside the arg → escaped via close-quote, escaped quote, reopen.
	if !strings.Contains(cmd, `'echo "it'\''s fine"'`) {
		t.Errorf("inner-quote escape wrong: %q", cmd)
	}
}
