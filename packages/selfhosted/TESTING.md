# Testing a self-hosted install

The full run on a real machine and a real domain, driven by the `nvoi` CLI as an installer and their team use it:
what you give, the commands in order, what each check expects, and the cleanup. Curl checks only what the CLI
cannot: the certificates, and a preview's answer without a token.

```
 you give               the server runs              you check (nvoi CLI)
 ────────               ───────────────              ────────────────────
 a server, ports   ──►  init, caddy, nvoi,      ──►  certificates, /up, signup, approval, login,
 80/443 open            db, forgejo                  repo host + git push, a session,
 a domain (3 names)                                  previews off: 501 with the reason
 a Daytona key          + previews (Cloudflare) ──►  preview through the worker, terminal
```

## 1. What you create first

1. **A server** with Docker (compose) and a public address, ports 80 and 443 open (the smallest cloud server
   does: 2 cores, 4 GB).
2. **A domain**, and three DNS records to the server's address: `<host>`, `git.<host>`, `mcp.<host>` (README.md,
   Names). On Cloudflare, make them DNS only (grey): Caddy gets the certificates itself.
3. **A Daytona API key** (app.daytona.io > Keys), for `SANDBOX_CREDENTIALS`.
4. **A model credential for the agent**: a Claude subscription's OAuth token or an Anthropic API key.
5. **The image**: `ghcr.io/getnvoi/core` if published, else built from the repository's root and loaded on the
   server (`docker buildx build -f packages/selfhosted/Dockerfile -t ghcr.io/getnvoi/core:latest .`, and
   `-f packages/selfhosted/Dockerfile.previews -t ghcr.io/getnvoi/core-previews:latest` for previews).
6. **On your machine**: the `nvoi` CLI (`curl -fsSL https://cli.nvoi.to/install.sh | sh`), git, curl, openssl.

For previews: the domain's zone on Cloudflare, its account id, and an API token with Workers Scripts Edit (account),
Zone Read, DNS Edit and Workers Routes Edit (zone), and a fourth record, `sandbox.<host>`, to the server.

## 2. Without previews

`H` is `NVOI_HOST`. On the server, in a folder holding `compose.yml`:

1. `.env` with the three lines (README.md), then `docker compose up -d`. Expect `docker compose ps -a`: init
   exited 0, caddy up, nvoi, db and forgejo healthy. `docker compose logs init` names six secrets "made".
2. **Certificates**: `echo | openssl s_client -connect $H:443 -servername $H | openssl x509 -noout -issuer`, and
   the same for `git.$H` and `mcp.$H`. Expect Let's Encrypt for each.
3. **The site**: `curl -sS https://$H/up` answers 200. A name that is not the installation's gets no certificate.
4. **Signup**: `nvoi signup --endpoint https://$H --email you@… --password '<12+ characters>' --workspace test`.
   `nvoi login --endpoint https://$H --email … --password …` says the account is pending approval.
5. **Approval**, on the server: `docker compose exec nvoi /app/bin/nvoi rpc 'Nvoi.User.approve!("you@…")'`.
6. **Login**: the same `nvoi login`, then `nvoi whoami` names the workspace.
7. **A hosted repository**: `nvoi repo host --name demo`, `nvoi token create --name push` (its secret shows once).
   In a small project with a commit: `git push https://git.$H/<workspace>/demo.git main`, with any user name and the
   token as the password. `nvoi repo list` shows it.
8. **The model credential**:
   `nvoi llm-config create --provider claude_code --label test --values '{"kind":"oauth","token":"…"}'`.
9. **A session**: `nvoi environment list <repository-id>`, then
   `nvoi environment-command set <environment-id> --kind preview --name web --command "node -e \"require('http').createServer((q, r) => r.end(require('fs').readFileSync('index.html'))).listen(3000)\""`
   (the project holds an `index.html`; the machine's image has Node, not Python) and `nvoi environment update <environment-id> --preview_port 3000`. Then
   `nvoi session start <repository-id> --prompt "Say hello."` and `nvoi session follow <session-id>`. Expect the
   Daytona machine to start (the seed first, then the fork) and the turn to end with an answer.
10. **Previews off**: `nvoi preview show <session-id>` and `nvoi terminal start <session-id>` answer 501
    `previews_not_set_up`: "Previews are not set up on this server. Its admin turns them on with the previews
    service in compose.yml (Cloudflare)."
11. **Secrets kept**: `docker compose restart`, then `docker compose up -d`: `docker compose logs init` names six
    secrets "kept", and the login of step 6 still works.
12. **GitHub off**: `nvoi github-installation list` answers 501 with the code `github_not_set_up` and the reason.
    `curl -s -X POST https://$H/webhooks/github` answers the same.
13. **GitHub on (optional)**: make a test GitHub App and put its six variables in `.env` (README.md, "GitHub
    (optional)"), then `docker compose up -d`. `nvoi github-installation list` answers `[]`.
    `nvoi github-install-intent start` answers a `github.com` link; open it, install the App on a test account,
    and expect the browser to come back to `https://$H/oauth/github/callback` with "Connected <account>".
    `nvoi github-installation list --table` then shows the account and its repositories. Remove the App's
    installation on GitHub afterwards, and the six variables from `.env`.

## 3. With previews

1. `CLOUDFLARE_API_TOKEN=… CLOUDFLARE_ACCOUNT_ID=… docker compose --profile previews run --rm previews`. Expect the
   route and the wildcard record found or made, the worker deployed with its gate secret, then "previews are on".
2. Run it again: the same names found, nothing made; it deploys the worker's code again and nothing else.
3. `nvoi preview start <worktree-id>`, then `nvoi preview show <session-id>` until it is running. Open the address
   it gives (it carries a signed token): expect the checkout's page. `curl -sI https://<slug>-preview.$H/` without a
   token answers 401: that is the worker on its route.
4. `nvoi terminal start <session-id>` answers the socket (`wss://<slug>-preview.$H/…`), a token and the shells.

Report each check as passed or failed, with the command's output for a failure.

## 4. Cleanup

1. `docker compose down -v`: stops the services and removes every volume, the secrets included.
2. The Daytona machines: delete the sandboxes labelled with this installation (`nvoi-installation`), or those
   named `seed-…` and after the session's slug.
3. With previews: delete the worker `nvoi-<host with dashes>-preview` (its route goes with it), and the wildcard
   record `*.<domain>` if the previews service made it (its comment is "nvoi previews").
4. The DNS records you made, and the server.
