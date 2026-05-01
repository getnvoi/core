package sshfake

import (
	"context"
	"io"
	"strings"
)

type Response struct {
	Stdout []byte
	Err    error
}

type Match struct {
	Contains string
	Resp     Response
}

type Shell struct {
	Address  string
	Matchers []Match
	Default  Response
	Calls    []string
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
