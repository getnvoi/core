# Self-hosted nvoi

The core of nvoi (`packages/core`), run on your own machines: agents working on your hosted repositories, on
your own sandbox provider, reached on your own domain. No billing. Repositories are pushed with plain git to
`git.<your host>`. GitHub is optional: it is on once you give the server a GitHub App of your own
([GitHub](#github-optional)).

## What you need

- A machine with Docker (compose) and a public address, ports 80 and 443 open.
- A domain whose names point at that address (DNS records below).
- A sandbox provider's credentials, for the agents' machines: Daytona today.

## Install

```
cp env.example .env      # then fill in the three lines
docker compose up -d
```

`.env` holds three lines:

| Variable | What |
| --- | --- |
| `NVOI_HOST` | the name the installation answers on (`acme.dev`, or `nvoi.acme.com`) |
| `SANDBOX_PROVIDER` | `daytona` |
| `SANDBOX_CREDENTIALS` | the provider's API key (several values are joined by `:`) |

The services:

```
 internet ──► caddy :80 :443 ──► nvoi :4000 ──► db (Postgres)
              (certificates)       │       └──► forgejo (hosted repositories)
                                   └── /secrets ◄── init (bin/secrets, once)
```

1. `init` writes the other secrets into the `secrets` volume, one file each, mode 600: the database's and
   Forgejo's passwords, the key that signs sessions, the encryption keys and salt, and the gate secret. A file
   that exists is kept, so every later `up` changes nothing.
2. `db` and `forgejo` start with their passwords from that volume.
3. `nvoi` migrates its database, then serves. It reads each secret from its variable first, then from its file in
   `/secrets` (the variable's name in lower case), and names any that is missing at boot.
4. `caddy` gets a certificate from Let's Encrypt for a name when that name is first asked for, and only after
   the server says the name is the installation's (`GET /tls?domain=…`). Other names get no certificate.

Then sign up at `https://<your host>` with the `nvoi` CLI:
`nvoi signup --endpoint https://<your host> --email … --password … --workspace …`.

## Accounts

Every new account waits for approval. Approve one from the server:

```
docker compose exec nvoi /app/bin/nvoi rpc 'Nvoi.User.approve!("person@acme.dev")'
```

## Names

| | `NVOI_HOST=acme.dev` | `NVOI_HOST=nvoi.acme.com` |
| --- | --- | --- |
| The app and its API | `acme.dev` | `nvoi.acme.com` |
| Git | `git.acme.dev` | `git-nvoi.acme.com` |
| MCP | `mcp.acme.dev` | `mcp-nvoi.acme.com` |
| The preview worker's questions (previews only) | `sandbox.acme.dev` | `sandbox-nvoi.acme.com` |
| Previews (previews only) | `*-preview.acme.dev` | `*-preview-nvoi.acme.com` |

Point the first three at the machine (an A or AAAA record each), and the sandbox name too once previews are on.
Each name is one label under the domain, so one wildcard certificate could cover them all; Caddy gets one per name.

## Previews

A session's preview, an app artifact and the Terminal go through the preview worker, a Cloudflare Worker. They are
off until you turn them on, and their operations answer `501 previews_not_set_up` with the reason meanwhile.
Everything else works without them. To turn them on, the domain's zone must be on Cloudflare:

```
CLOUDFLARE_API_TOKEN=… CLOUDFLARE_ACCOUNT_ID=… docker compose --profile previews run --rm previews
```

The token needs Workers Scripts Edit on the account, and Zone Read, DNS Edit and Workers Routes Edit on the zone.
The `previews` service deploys the worker with the gate secret, its route (`*-preview.<host>/*`) and a proxied
wildcard record (`*.<domain>`, no origin: the worker answers), each found by its name first. Then it writes the
`previews` file the server reads at once. A name under a zone (`nvoi.acme.com`) would claim the whole zone's
wildcard: add `PREVIEWS=wildcard` to ask for it. Run it again after an upgrade: it deploys the worker's new code and
changes nothing else.

## Images (production environments)

Production environments build images on a build machine and deploy them to a cluster. Both move the image through
a bucket, by URLs signed for one object, so the bucket must be reachable from the internet. Set
`NVOI_BUCKET`, `NVOI_BUCKET_ENDPOINT_URL`, `NVOI_BUCKET_ACCESS_KEY_ID` and `NVOI_BUCKET_SECRET_ACCESS_KEY`
(`NVOI_BUCKET_REGION` too when it is not `auto`) in `.env` for any S3-compatible bucket. Without them, building and
deploying images answer `501 bucket_not_set_up`; nothing else needs the bucket.

## Kubernetes

The same model in `kubernetes/`: `kubectl create namespace nvoi`, `kubectl -n nvoi create secret generic nvoi
--from-env-file=.env`, set your names in the Ingress of `kubernetes/nvoi.yaml`, then `kubectl -n nvoi apply -f
kubernetes/nvoi.yaml`. The Job `secrets` makes the Secret `nvoi-secrets` once; the pods mount it at `/secrets`.
Previews: `kubernetes/previews.yaml` says how.

## GitHub (optional)

Without a GitHub App the server runs on hosted repositories only. Every GitHub operation (`nvoi github-installation
…`, `nvoi github-install-intent …`, `nvoi github-sign-in …`, `nvoi repo create`) and GitHub's webhook and callback
answer `501` with the code `github_not_set_up` and the reason. The boot check does not ask for the GitHub variables.

With an App, people connect GitHub accounts, import their repositories, sign in with GitHub (the app and the MCP
page), and tasks listen for GitHub's events. GitHub is on when all six variables are set; one missing leaves it off.

### 1. Create the App

On GitHub: Settings > Developer settings > GitHub Apps > New GitHub App (or the same under an organization's
settings). `H` is `NVOI_HOST`.

| Setting | Value |
| --- | --- |
| Homepage URL | `https://H` |
| Callback URL | `https://H/oauth/github/callback` |
| Request user authorization (OAuth) during installation | on |
| Expire user authorization tokens | either |
| Webhook | active, URL `https://H/webhooks/github`, a secret you make (`openssl rand -hex 32`) |
| Where can this GitHub App be installed? | any account, or only yours |

Repository permissions:

| Permission | Access | What for |
| --- | --- | --- |
| Contents | Read and write | the checkout, the manifest's files, pushing a branch |
| Workflows | Read and write | pushing a branch that changes `.github/workflows` |
| Pull requests | Read and write | opening a pull request; its events |
| Issues | Read-only | the issue events a task listens for |
| Metadata | Read-only | required by GitHub; the repository events |

Account permissions: Email addresses, Read-only (signing in matches a person by their verified addresses).

Subscribe to events: Pull request, Pull request review, Issues, Issue comment, Push, Repository. GitHub sends the
installation events on its own.

### 2. Give the server its credentials

From the App's page: its App ID, its Client ID, a client secret (Generate a new client secret), a private key
(Generate a private key, a `.pem` file), and its slug, the last part of its public link
(`https://github.com/apps/<slug>`).

```
GITHUB_APP_ID=123456
GITHUB_APP_SLUG=acme-nvoi
GITHUB_APP_CLIENT_ID=Iv23…
GITHUB_APP_CLIENT_SECRET=…
GITHUB_APP_WEBHOOK_SECRET=…        # the webhook's secret above
GITHUB_APP_PRIVATE_KEY=…           # the .pem on one line, its line breaks written \n
```

With compose, add them to `.env`; the key on one line is
`printf 'GITHUB_APP_PRIVATE_KEY=%s\n' "$(awk '{printf "%s\\n", $0}' app.pem)" >> .env`. Then
`docker compose up -d`. On Kubernetes, put them in a secret named `nvoi-github`, which the server reads when it
exists: `kubectl -n nvoi create secret generic nvoi-github --from-env-file=github.env`, then
`kubectl -n nvoi rollout restart deployment nvoi`.

Check it: `nvoi github-installation list` answers a list (empty at first) instead of `github_not_set_up`.

## Upgrading

Set `NVOI_VERSION` (compose) or the image's tag (Kubernetes) to the new version, then `docker compose up -d` or
`kubectl apply`. The server migrates its database before it starts.

## Backups

The `secrets` volume holds the keys that open what the database seals: without it, the installation is lost. Back
up the `secrets`, `db` and `forgejo` volumes together.

`TESTING.md` is the full run on a real machine and domain, with the cleanup.
