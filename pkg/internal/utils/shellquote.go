package utils

import "strings"

// ShellQuote wraps s in single quotes, escaping any internal single
// quotes via the standard `'\''` close-escape-reopen pattern. Result
// is always a valid single Bourne-shell token — safe to drop into
// any string passed to a remote shell (SSH, kubectl exec, etc.).
//
// We quote even shell-safe strings, which keeps the encoding uniform
// and the test matrix tiny (one rule, no edge cases). Algorithm
// matches Python's shlex.quote, Perl's String::ShellQuote::shell_quote,
// Ruby's Shellwords.escape — there is no other correct answer for
// POSIX single-quoting.
//
// Lives in utils/ so every package that builds remote-shell command
// strings (install, future cmd helpers, …) shares one definition
// instead of copy-pasting the body around the way upstream nvoi did.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
