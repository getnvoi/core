// Package sshfake is the test fake for ssh.Shell. Records every
// command run and returns canned responses based on substring match.
// First match wins. Anything unmatched returns Default (zero = success
// with empty output).
package sshfake

import (
	"context"
	"io"
	"strings"
)

// Response is the canned output for a matched command.
type Response struct {
	Stdout []byte
	Err    error
}

// Match pairs a substring with a response. Walked in order; first
// match wins. Substring (not prefix) so tests can match middle of
// long shell wrappers like `sudo bash -c "ETCDCTL_API=…"`.
type Match struct {
	Contains string
	Resp     Response
}

// Shell is a fake ssh.Shell. Construct directly:
//
//	sh := &sshfake.Shell{
//	    Address: "10.0.0.1:22",
//	    Matchers: []sshfake.Match{
//	        {Contains: "kubectl drain", Resp: sshfake.Response{}},   // success, empty
//	        {Contains: "member list",   Resp: sshfake.Response{Stdout: []byte("a1, started, foo, ...")}},
//	    },
//	}
type Shell struct {
	Address  string  // returned by Addr()
	Matchers []Match // first substring match wins
	Default  Response
	Calls    []string // every command run, in order
}

func (s *Shell) Addr() string { return s.Address }

func (s *Shell) Run(_ context.Context, cmd string) ([]byte, error) {
	s.Calls = append(s.Calls, cmd)
	r := s.lookup(cmd)
	return r.Stdout, r.Err
}

func (s *Shell) RunStream(_ context.Context, cmd string, stdout, _ io.Writer) error {
	s.Calls = append(s.Calls, cmd)
	r := s.lookup(cmd)
	if len(r.Stdout) > 0 && stdout != nil {
		_, _ = stdout.Write(r.Stdout)
	}
	return r.Err
}

func (s *Shell) lookup(cmd string) Response {
	for _, m := range s.Matchers {
		if strings.Contains(cmd, m.Contains) {
			return m.Resp
		}
	}
	return s.Default
}
