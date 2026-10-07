# MCP: nvoi as a remote MCP server

nvoi serves a public MCP server at `https://mcp.nvoi.to`. An MCP client (Claude, Claude Code, Cursor, the MCP
Inspector) connects to it and works in nvoi as one member, in one workspace. The client gets the member's role and
no more.

## Where it answers

```
https://mcp.nvoi.to/        the MCP endpoint (production)
https://mcp-dev.nvoi.to/    dev
http://mcp.localhost:4790/  a test stack
```

The router sends every request to the MCP host to `NvoiWeb.Mcp`; the host is `config :nvoi, mcp_host:`
(`docs/hosts.md`). On the MCP host nothing of the API answers. Every URL the server gives (metadata, OAuth
endpoints, the `WWW-Authenticate` header) is built from the host the request came in on.

## Connect a client

### With the CLI

The `nvoi` CLI connects Claude Code or Cursor in one command. Sign the CLI in first (`nvoi login`).

```sh
nvoi mcp add claude --context acme   # Claude Code, scope local: this folder (--scope user: every folder)
nvoi mcp add cursor                  # Cursor: ~/.cursor/mcp.json, every Cursor project
nvoi mcp add --print    # writes nothing; prints the token and both configurations
```

1. The CLI makes a personal token: `POST /api/v1/tokens` with the name `<client> (<host name>)` and the context's
   workspace (`--context`, required when the machine holds several). The request names the repository it runs in
   (`Nvoi-Repository`) and the context's workspace (`Nvoi-Workspace`): a context of another workspace is refused
   with `409 wrong_context` before anything is written. The token lasts 90 days (`--expires-in-days`, 1 to 365).
2. The CLI writes the server with that token as its bearer and the workspace as `Nvoi-Workspace`. For Claude Code,
   it runs `claude mcp add --transport http nvoi <url> --header "Authorization: Bearer <secret>" --header
   "Nvoi-Workspace: <slug>"`. For Cursor, it sets `mcpServers.nvoi` in `~/.cursor/mcp.json`, keeps every other
   entry, and writes the file with mode 600. `--scope project` is refused: it would write the secret into the
   repository's `.mcp.json`.
3. The CLI prints the token's name and id and where it wrote. It never prints the secret, except with `--print`.

The URL is the MCP host beside the endpoint: `https://mcp.nvoi.to` for `https://nvoi.to`, `https://mcp-dev.nvoi.to` for `https://dev.nvoi.to`, `http://mcp.localhost:<port>` for a test stack.
`--url` changes it. An `nvoi` server already in the client is replaced only with `--force`. When `claude` is not on
the PATH, the CLI prints the `claude mcp add` command to run. When the client refuses the server, the CLI revokes the
token. Revoke a token with `nvoi token delete <id>`.

The sections below connect a client without the CLI.

### Claude (claude.ai, the desktop app)

1. Open Settings → Connectors → Add custom connector.
2. Enter `https://mcp.nvoi.to`.
3. Claude opens nvoi's sign-in page. Sign in with your email and password, or with GitHub.
4. Pick the workspace and select Allow.

### Claude Code

With OAuth (Claude Code opens the browser):

```sh
claude mcp add --transport http nvoi https://mcp.nvoi.to
```

With a token (make one in nvoi, or with `POST /api/v1/tokens`):

```sh
claude mcp add --transport http nvoi https://mcp.nvoi.to --header "Authorization: Bearer $NVOI_TOKEN"
```

### Cursor

In `~/.cursor/mcp.json`:

```json
{
  "mcpServers": {
    "nvoi": {
      "url": "https://mcp.nvoi.to",
      "headers": { "Authorization": "Bearer <your nvoi token>" }
    }
  }
}
```

Leave out `headers` to sign in through OAuth.

### MCP Inspector

```sh
npx -y @modelcontextprotocol/inspector --cli https://mcp.nvoi.to --transport http \
  --header "Authorization: Bearer $NVOI_TOKEN" --method tools/list
```

## Sign-in

Every message needs a bearer. Without one, the answer is `401` with
`WWW-Authenticate: Bearer resource_metadata="https://mcp.nvoi.to/.well-known/oauth-protected-resource"`. A client
reads that metadata and starts OAuth.

### A token

Any nvoi API token works as the bearer: the desktop's, the CLI's, or a personal token. A token names one sign-in
(a row of `sessions`) and one workspace. Deleting the row revokes the token.

### OAuth 2.1

nvoi is its own authorization server. The client is public: it holds no secret and proves each authorization
with PKCE (S256).

```
client                         mcp.nvoi.to                          person's browser
  | POST /  (no bearer)  ------> 401 + resource_metadata
  | GET /.well-known/oauth-protected-resource
  | GET /.well-known/oauth-authorization-server
  | POST /register  -----------> client_id (Nvoi.OauthClient)
  | open /authorize?... ------------------------------------------>  sign in (email + password, or GitHub)
  |                                                                  pick a workspace, Allow
  | <------------------------------ redirect_uri?code=...&state=...&iss=...
  | POST /token (code + code_verifier) -> access_token, refresh_token
  | POST /  (Bearer access_token) -> MCP
```

| Endpoint | What it does |
| --- | --- |
| `GET /.well-known/oauth-protected-resource` (and `/mcp` after it) | RFC 9728: the resource and its authorization server. |
| `GET /.well-known/oauth-authorization-server` | RFC 8414: the endpoints, `code`, S256, no client secret. |
| `POST /register` | RFC 7591: a client registers its name and its redirect addresses (https, loopback http, or an app's own scheme). |
| `GET /authorize` | Checks the client, the redirect address and the challenge, then shows the sign-in page. |
| `POST /authorize/password` | Signs in with an email and a password, then shows the workspace choice. |
| `GET /authorize/github` | Signs in with GitHub, when GitHub is on (`docs/github.md`). GitHub sends the person back to nvoi's callback (`/oauth/github/callback` on the API host), which shows the workspace choice. |
| `POST /authorize/decide` | Allow sends a code (one minute, one use) to the client; Deny sends `access_denied`. |
| `POST /token` | `authorization_code` (with `code_verifier`) or `refresh_token`. Answers `access_token`, `refresh_token`, `expires_in`. |
| `POST /revoke` | RFC 7009: revokes the sign-in an access or a refresh token names. |

The access token is an nvoi API token. It names an `oauth` sign-in, named after the client, pinned to the workspace
the person picked. It also works on `/api/v1`. It lasts 30 days. Each refresh gives a new refresh token and starts
the 30 days again. The person sees the client in their tokens and can revoke it there.

The pages carry no state on the server: the checked request is signed into each page and expires after 15 minutes.
The GitHub sign-in uses a `github_sign_ins` row with the purpose `connect`, the same PKCE verifier and nonce as
the desktop's sign-in.

## Who may decide

People decide; tools only act. A decision is allowing an approval-level action, allowing or denying an agent's
held call, or answering an agent's question. Only a person's own sign-in decides: the desktop app or the CLI after
`nvoi login` (kind `app`). A personal token (a script, CI, an MCP client set up by `nvoi mcp add`) and a client
connected through OAuth act for the member, but a decision from them is refused with `403 person_only`.

A client connected through OAuth is also confined to this server: its token on `/api/v1`, git and the socket is
refused with `403 mcp_only`. A personal token keeps working on the API and here, for scripts. Neither makes a token:
making one, switching workspace or opening one is a person's own sign-in's (`403 person_only`), so a token never
outlives its expiry or reaches another workspace of its person. Such a token sees and revokes the tokens of its own
workspace only, and `whoami` lists that workspace alone.

## The workspace a client expects

A token is pinned to one workspace. A client says which it expects with `Nvoi-Workspace: <slug>` (what `nvoi mcp
add` writes) or `Nvoi-Repository: <nvoi remote>`; a token of another workspace is refused with `409 wrong_context`,
naming both slugs and how to set the server up again. The `initialize` instructions name the workspace and its slug,
and tell the model to act on nothing when the repository it works in is another workspace's.

| Sign-in | Acts | Decides | `/api/v1` |
|---|---|---|---|
| app (desktop, CLI) | yes | yes | yes |
| personal token | yes | no | yes |
| OAuth client | yes, through MCP | no | no |

## Tokens

A person manages their own sign-ins through the API (`token` group, `docs` in `priv/openapi.yaml`):

- `POST /api/v1/tokens` `{name, workspace?, expires_in_days?}`: a personal token. The answer has its `secret` once.
  `workspace` is a slug of one of the caller's workspaces (the current one when absent); `expires_in_days` is 1 to
  365 (90 when absent).
- `GET /api/v1/tokens`: the caller's sign-ins that have not expired: `app` (desktop, CLI login), `personal`, and
  `oauth` (connected clients). Each has `id`, `kind`, `name`, `user_agent`, `workspace`, `created_at`,
  `last_used_at`, `expires_at` and `current`. Never a secret.
- `DELETE /api/v1/tokens/{id}`: revokes one of the caller's own and closes its sockets. Another person's is `404`.

Every request marks its sign-in used (`last_used_at`, at most once a minute). A sign-in past its `expires_at` is
refused by the API, the socket and the MCP server.

## The transport

Streamable HTTP, protocol versions `2025-11-25`, `2025-06-18` and `2025-03-26`, without sessions:

- `POST` one JSON-RPC message (or a batch, from older clients). A request gets its answer as JSON; when the client
  accepts only `text/event-stream`, as one event. A notification or a response gets `202`.
- `GET` and `DELETE` are `405`: the server opens no stream of its own.
- A `MCP-Protocol-Version` header with an unknown version is `400`.
- Methods: `initialize`, `ping`, `tools/list`, `tools/call`. Anything else is `-32601`.

## The tools

The three operations of the gateway (`Nvoi.Tools.Gateway`, `tools.md`), each run through the same action as
`/api/v1/gateway/*`:

| Tool | Input | What it does |
| --- | --- | --- |
| `search` | `query`, `limit` (1–25) | The actions that do what the words say, best first, each with the member's level. Only actions the member may use. |
| `describe` | `name` | One action: its input schema, roles, level, route, and whether repeating it is safe. |
| `execute` | `name`, `params`, `page` | Runs the action as the member. A change at the level `approval` is refused (`approval_required`): a person allows it in nvoi, never through MCP, since the client is a model. |

A call is the member's own:

- The role, the member's grants and the system agent's levels apply, as for any gateway call.
- Sign-in, credentials, members, billing and workspaces are refused (`agent_refused`): a member does them in nvoi.
- A refusal is a tool result with `isError: true`. Its text is `Refused (<status> <code>): <the API's sentence>`;
  `structuredContent` has `error`, `code` and `status`.
- Each call goes through `Nvoi.Tools.Action.run`, so it emits `[:nvoi, :tool, :call]` and writes activities: one
  for the gateway tool, one for the action `execute` ran.

## Code

- `lib/nvoi_web/mcp.ex`: the routes, the base URL, body parsing, CORS.
- `lib/nvoi_web/mcp/host.ex`: the host switch in the endpoint.
- `lib/nvoi_web/mcp/server.ex`: JSON-RPC and the transport.
- `lib/nvoi_web/mcp/tools.ex`: the three tools.
- `lib/nvoi_web/mcp/oauth.ex`, `lib/nvoi_web/mcp/authorize.ex`: OAuth.
- `lib/nvoi/oauth_client.ex`, `lib/nvoi/oauth_code.ex`, `lib/nvoi/session.ex`: the records.
- `lib/nvoi/tools/token/*`: the token API.

## Left to do

- Production: route `mcp.nvoi.to` to the server (DNS, the tunnel or the load balancer, the certificate). The
  server needs nothing more: the host is `mcp.nvoi.to` by default.
- GitHub: the callback is `NvoiWeb.Endpoint.url()` + `/oauth/github/callback`. That address must be one of the
  GitHub App's callback URLs (it is the same one the desktop's sign-in uses when it calls that host).
- The desktop's page for tokens (list, make, revoke).
- Clients registered and never used stay in `oauth_clients`; a cleanup job can remove the ones with no sign-in.
