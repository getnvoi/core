# ALWAYS LINUX, NEVER THE HOST'S OS.
#
# `go build` with no GOOS targets the machine you are standing on, so a bare
# build on a Mac produces a Darwin binary — which uploads fine, and then dies as
# "cannot execute" inside the box, long after the step that was actually wrong.
#
# Two architectures because the box's own is not predicted here:
# `packages/sandbox/Dockerfile` fetches with `uname -m` at build time, whatever
# that turns out to be.
#
# Static, so a box needs no libc we did not choose.
DIST := dist

# NAMED AS `uname -m` SAYS IT, not as Go's GOARCH does. The Dockerfile fetches
# `boxd-linux-$(uname -m)`, and a box that says `x86_64` cannot find `amd64`.
build: $(DIST)/boxd-linux-x86_64 $(DIST)/boxd-linux-aarch64

# PREREQUISITES, or these never rebuild. A file target with no prerequisites is
# up to date the moment it exists, so a build after editing did nothing and the
# box ran last week's agent — silently, because a stale binary starts perfectly.
SOURCES := $(shell find . -name '*.go') go.mod $(wildcard go.sum)

$(DIST)/boxd-linux-x86_64: $(SOURCES)
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o $@ ./cmd/boxd

$(DIST)/boxd-linux-aarch64: $(SOURCES)
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o $@ ./cmd/boxd

test:
	go test ./...

clean:
	rm -rf $(DIST)

.PHONY: build test clean
