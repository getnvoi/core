# docker.io/nvoi/db — the uniform database backup AND restore runner.
#
# Every DatabaseProvider's backup CronJob runs this image in MODE=backup
# (default). `nvoi database restore` and `nvoi database migrate` launch
# one-shot Jobs with MODE=restore. Same image, two pipelines —
# dispatches on ENGINE to pick the dump/restore tool (pg_dump/psql for
# postgres today); gzips dumps going out, gunzips coming back; uses the
# nvoi SigV4 client for S3-compatible I/O (no aws-cli dep).
#
# Multi-stage because the Go binary's compile-time deps are far heavier
# than the runtime image needs. Alpine for runtime keeps the image
# under 100 MB with postgresql-client installed.
#
# Tag discipline: published on every `v*` git tag by
# .github/workflows/release.yml; the nvoi CLI pins
# `docker.io/nvoi/db:<providers.DBImageTag>` so the image version and
# the CLI binary version always match. The image binary itself carries
# no version string — the tag IS the version.

ARG GO_VERSION=1.25
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/nvoi-db ./cmd/db

FROM alpine:3.21
# pg_dump must be >= the postgres server version, so we pin
# postgresql17-client (Alpine 3.21 ships it). 3.19's postgresql16-client
# can't dump a 17.x server — it aborts with `server version mismatch`.
# Bump in lockstep when nvoi starts supporting a newer postgres major.
RUN apk add --no-cache \
      ca-certificates \
      postgresql17-client
COPY --from=build /out/nvoi-db /usr/local/bin/nvoi-db
# No shell entrypoint — the Go binary handles env validation, dispatch,
# dump, restore, and upload. Direct exec means failures propagate as
# the binary's exit code, which is what k8s' BackoffLimit keys on.
ENTRYPOINT ["/usr/local/bin/nvoi-db"]
