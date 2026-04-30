package utils

// ResolveVar expands a `$VAR` reference against the supplied getenv
// function: `"$FOO"` → `getenv("FOO")`. Anything else passes through
// unchanged. The empty string and the bare `"$"` also pass through
// (no leading-only `$`).
//
// Used by callers that resolve YAML-supplied credential references
// at the cmd/ boundary — same pattern for registry: creds, future
// CredentialSource backends, etc. The getenv parameter (vs. reading
// os.Getenv directly) keeps this package pure and testable.
func ResolveVar(s string, getenv func(string) string) string {
	if len(s) > 1 && s[0] == '$' {
		return getenv(s[1:])
	}
	return s
}
