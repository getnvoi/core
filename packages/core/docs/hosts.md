# Hosts

Every part of nvoi a client reaches has a name of its own. The app answers on the site's name; git and MCP each
answer on theirs, and nothing is served under a path for them. Production also refuses what a name is not for.
Dev and test stacks enforce none of that and pass every request through.

## The names, per build

| Part | Production | Dev (`bin/dev`) | Test stack (`bin/stack`) | Tests |
| --- | --- | --- | --- | --- |
| The app: API, site, docs, legal pages, OAuth callbacks, webhooks, socket | `nvoi.to` | `dev.nvoi.to` | `127.0.0.1:4790` | `www.example.com` |
| Git for hosted repositories (`NvoiWeb.Git`) | `git.nvoi.to` | `git-dev.nvoi.to` | `git.localhost:4790` | `git.nvoi.to` |
| The MCP server and its OAuth (`NvoiWeb.Mcp`) | `mcp.nvoi.to` | `mcp-dev.nvoi.to` | `mcp.localhost:4790` | `mcp.nvoi.to` |
| The preview Worker's questions: `/route`, `/boot`, `/touch` (`NvoiWeb.EdgeController`) | `sandbox.nvoi.to` | `dev.nvoi.to` | none | `sandbox.nvoi.to` |
| Previews, served by the Worker, never by the app | `*-preview.nvoi.to` | `*-preview-dev.nvoi.to` | none | none |

`*.localhost` always resolves to the machine itself in curl, which git uses, so a test stack needs no DNS.

The CLI finds the git and MCP names from the API endpoint with the same rule (`Beside` in
`packages/clients/cli/internal/cmd/git.go`):
- `nvoi.to` gives `git.nvoi.to`;
- `dev.nvoi.to` gives `git-dev.nvoi.to`;
- a loopback address gives `git.localhost`.

The scheme and the port are kept.

## What each name answers in production

| A request to… | Answer |
| --- | --- |
| `nvoi.to` | the app's routes |
| `git.nvoi.to`, `mcp.nvoi.to`, `sandbox.nvoi.to` | its own part only; anything else is a 404 |
| `*-preview.nvoi.to` | passes (the Worker answers these before the app sees them) |
| any other `*.nvoi.to` (`www`, a typo) | a permanent redirect (301) to `https://nvoi.to`, same path and query |
| a bare address (the pods' health checks) | `/up` only; anything else is a 404 |
| the live socket (`/socket`) | accepted on `nvoi.to` only |

## Where each piece is declared

| What | Where |
| --- | --- |
| Production's names | `config/config.exs` (`git_host`, `mcp_host`); `config/prod.exs` (`site_host`, `sandbox_host`, `preview_suffix`, `production: true`) |
| Dev's and a stack's names | `config/dev.exs`, from `NVOI_HOST`: `dev.nvoi.to` gives the `-dev` names, none gives the `.localhost` names |
| Which name serves what | `NvoiWeb.Router`: one `scope host:` per part, in every build |
| Production's redirect and 404 rules | `NvoiWeb.Hosts`, in the site routes' pipeline; it passes everything outside production |
| The socket's rule | `NvoiWeb.Hosts.socket?/1`, asked by `NvoiWeb.UserSocket` |
| Production's DNS and tunnels | tofu (`infra/production/tofu`): `nvoi.to` and `*.nvoi.to` to the app's tunnel; `sandbox.nvoi.to` to the preview tunnel; `*-preview.nvoi.to` to the Worker's route |
| Dev's DNS and tunnel | tofu (`infra/dev/tofu`, applied by `bin/dev`; `bin/dev plan` shows the difference): `dev.nvoi.to`, `git-dev.nvoi.to` and `mcp-dev.nvoi.to` to the laptop's tunnel, each with its own record, which is more specific than production's wildcard |

## Why the sandbox has a name of its own

The preview Worker asks the app `/route`, `/boot` and `/touch` once per page, and again for each file a preview loads.
If those calls went to `nvoi.to`, they would queue behind people's requests in the same tunnel, and a flood of
preview traffic would take the site down. So previews get their own tunnel, connectors and name. The server behind
both tunnels is the same.
