package build

// Preflight in DockerRunner shells out to `docker info` and `docker
// buildx version` — those are integration concerns that exercise the
// real docker CLI. Skipped in unit tests.
//
// The build-phase logic that DOES belong in unit tests
// (login-per-host, build ordering, fail-fast) lives in build_test.go
// against a fake Runner. Auth-config-on-disk parsing is gone:
// credentials now come from the YAML registry: block via Login.
