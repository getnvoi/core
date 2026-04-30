# syntax=docker/dockerfile:1.7
#
# cmd/web — public site for the nvoi CLI. Built from repo root via
# `nvoi.yaml`'s services.web.build:
#   build: {context: ".", dockerfile: "cmd/web/Dockerfile"}
#
# Two stages, distroless final, pure-Go (CGO_ENABLED=0) so the
# resulting image is static and runs under :nonroot without an OS
# layer. Postgres driver is pgx (via gorm.io/driver/postgres) — no
# libpq, no system deps.

FROM golang:1.26-alpine AS builder
WORKDIR /src

# Layer-cache the dep download independently of source changes.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the module. cmd/web depends on go.mod / go.sum
# from the root; copying everything is the simplest correct answer.
COPY . .

ENV CGO_ENABLED=0 GOOS=linux
RUN go build \
    -trimpath \
    -ldflags='-s -w' \
    -o /out/web \
    ./cmd/web

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=builder /out/web /web
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/web"]
