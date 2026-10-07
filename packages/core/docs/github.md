# GitHub

GitHub is part of the core and optional. It is on when the installation has the credentials of a GitHub App, and off
otherwise. nvoi.to's sealed credentials hold its App, so GitHub is on there. A self-hosted installation turns it on
with its own App (`packages/selfhosted/README.md`).

## The switch

`Nvoi.GitHub.enabled?/0` is the only answer to "is GitHub on". It is true when every key of the App resolves through
`Nvoi.Credentials`: the environment variable named from the path first, then the sealed file.

| Key | Path | Variable |
| --- | --- | --- |
| The App's id | `github_app.id` | `GITHUB_APP_ID` |
| Its slug (`github.com/apps/<slug>`) | `github_app.slug` | `GITHUB_APP_SLUG` |
| Its client id | `github_app.client_id` | `GITHUB_APP_CLIENT_ID` |
| A client secret | `github_app.client_secret` | `GITHUB_APP_CLIENT_SECRET` |
| Its webhook secret | `github_app.webhook_secret` | `GITHUB_APP_WEBHOOK_SECRET` |
| A private key (PEM) | `github_app.private_key` | `GITHUB_APP_PRIVATE_KEY` |

The private key may be on one line with its line breaks written `\n`. A missing key leaves GitHub off. The boot check
(`Nvoi.Credentials.check!/0`) never asks for these keys.

nvoi.to has two Apps. The sealed file's `github_app` is production's. The dev App is `dev.github_app`: `bin/env dev`
prints it as the `GITHUB_APP_*` variables, which win over the file, and `bin/dev`, `bin/stack` and `bin/server` give
them to the server.

## What GitHub adds when it is on

```
Nvoi.GitHub.enabled?()
  ├─ Nvoi.Source.of/1          a repository with host "github" → Nvoi.Source.GitHub
  ├─ Nvoi.Connectors.all/0     [Nvoi.Connectors.GitHub]: its deliveries, the events a task listens for
  ├─ Nvoi.Workspace.onboarding "github" first, done once connected (Nvoi.Connectors.GitHub.done?/1)
  └─ NvoiWeb.Mcp.Authorize     "Sign in with GitHub" on the MCP page (/authorize/github)
```

When it is off, none of them exist: onboarding starts on the repository, the connectors list is empty, and the MCP
page signs in with a password only.

## What stays declared

The routes and the tools are always declared, so the API document and the CLI are the same on every installation.
When GitHub is off, each one refuses with `501 {"error": "GitHub is not set up on this server. …", "code":
"github_not_set_up"}` (`Nvoi.GitHub.off/0`):

- the tools `github_installation_*`, `github_install_intent_*`, `github_sign_in_*` and `repo_create` (importing a
  GitHub repository);
- the webhook, `POST /webhooks/github` (and `POST /hooks/github`);
- the callback, `GET /oauth/github/callback`.

A GitHub repository's source refuses the same way, so a call that reaches one answers the reason.

## The code

- `lib/nvoi/github.ex`: the switch, the refusal, GitHub's REST API.
- `lib/nvoi/github_app.ex`: the App's keys, its JWT, the signed states, the authorize and install URLs.
- `lib/nvoi/github_installation.ex`, `github_repository.ex`, `github_install_intent.ex`, `github_sign_in.ex`: the
  records.
- `lib/nvoi/source/github.ex`, `lib/nvoi/connectors/github.ex`, `lib/nvoi/jobs/github_sync_job.ex`.
- `lib/nvoi_web/controllers/github_apps_controller.ex` (the callback), `github_sign_ins_controller.ex`,
  `lib/nvoi_web/mcp/github_sign_in.ex`.
- `lib/nvoi/tools/github_*`, `lib/nvoi/tools/repo/create.ex`.
- Tests turn GitHub on with stand-in credentials (`setup {Nvoi.GitHubFixtures, :on}`); `GitHubOffTest` checks the
  refusals with it off.
