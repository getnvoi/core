# nvoi

nvoi runs coding agents for teams. An agent works on a branch on a machine of its own, the team watches the same
session and talks to it, and a person approves anything that leaves the machine.

This repository is nvoi's core, published for self-hosting: the server, the daemon on every machine, the preview
worker, the CLI and the docs.

## Install

You need a server with Docker and ports 80 and 443 open, a domain pointed at it, and a Daytona API key.

```sh
git clone https://github.com/getnvoi/core && cd core/packages/selfhosted
cp env.example .env      # NVOI_HOST, SANDBOX_PROVIDER, SANDBOX_CREDENTIALS
docker compose up -d
curl -fsSL https://cli.nvoi.to/install.sh | sh
nvoi signup --endpoint https://<your host> --email … --password … --workspace …
docker compose exec nvoi /app/bin/nvoi rpc 'Nvoi.User.approve!("<your email>")'
```

The Self-hosting page walks through every step, the names to point at the server, GitHub, previews, upgrades,
backups and uninstalling: https://nvoi.to/docs/nvoi/self-hosting/ (and `/docs/nvoi/self-hosting/` on your own
installation). `packages/selfhosted/README.md` holds the same and Kubernetes.

## What is here

| Path | What |
| --- | --- |
| `packages/core` | the server (Elixir, Phoenix): the API under `/api/v1`, sessions, machines, the agents' turns |
| `packages/core/lib/compute` | the machine providers, production clusters and database vendors the server calls |
| `packages/core/lib/llm` | the agents' runners (Claude Code, Codex) |
| `packages/core/box` | `boxd`, the daemon on every machine (Go) |
| `packages/core/worker` | the preview worker (Cloudflare Workers) |
| `packages/clients/cli` | the `nvoi` CLI (Go) |
| `packages/core/site` | the docs site, served at `/docs` |
| `packages/selfhosted` | the images' Dockerfiles, `compose.yml`, `kubernetes/` and `previews` |

The images are `ghcr.io/getnvoi/core` and `ghcr.io/getnvoi/core-previews`, tagged with each release (`d1635d40` for this one) and `latest`.

Each commit here is one release, published from nvoi's own repository; its tag names the release.

## License

GNU Affero General Public License, version 3 only (AGPL-3.0-only): `LICENSE`. Contributing: `CONTRIBUTING.md`.
