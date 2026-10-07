# syntax=docker/dockerfile:1
# The self-hosted image: the core (packages/core) released alone, with the binary every machine runs (boxd) built
# for linux beside it. No sealed file: its secrets come from the folder bin/secrets writes and from .env
# (README.md). Built from the
# repository root: `docker buildx build -f packages/selfhosted/Dockerfile .`.

ARG ELIXIR=elixir:1.20.4-otp-29
# On the builder's own platform: boxd cross-compiles, so a multi-platform build never emulates Go.
FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS go
WORKDIR /src
COPY packages/core/box packages/core/box
# The machines are linux/amd64 whatever the node building this is.
RUN cd packages/core/box && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/boxd ./cmd/boxd

FROM ${ELIXIR} AS build
ENV MIX_ENV=prod MIX_HOME=/mix HEX_HOME=/mix/hex
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends git build-essential \
 && rm -rf /var/lib/apt/lists/* && mix local.hex --force && mix local.rebar --force
WORKDIR /app/core
COPY packages/core/mix.exs packages/core/mix.lock ./
COPY packages/core/config/config.exs packages/core/config/prod.exs config/
RUN mix deps.get --only prod && mix deps.compile
COPY packages/core/lib lib
COPY packages/core/priv priv
# bin/secrets, beside bin/nvoi in the release.
COPY packages/core/rel rel
COPY packages/core/config/runtime.exs config/
RUN mix compile && mix release

# The same Debian as the Elixir image the release was built in (trixie): the BEAM links against its glibc.
FROM debian:trixie-slim
# git for checkouts and the forge, openssl and ca-certificates for every provider, locales for the BEAM, psql to
# load the schema into a new database (Nvoi.Release).
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends \
      git openssl ca-certificates locales libstdc++6 libsctp1 openssh-client postgresql-client \
 && rm -rf /var/lib/apt/lists/* && sed -i '/en_US.UTF-8/s/^# //' /etc/locale.gen && locale-gen
ENV LANG=en_US.UTF-8 LC_ALL=en_US.UTF-8 MIX_ENV=prod PHX_SERVER=true HOME=/app
WORKDIR /app
COPY --from=build /app/core/_build/prod/rel/nvoi ./
COPY --from=go /out/boxd bin/linux/
ARG VERSION=dev
ENV VERSION=${VERSION}
EXPOSE 4000
CMD ["/app/bin/nvoi", "start"]
