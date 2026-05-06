# CLAUDE.md — nvoi

YAML → tofu (OpenTofu, MPL-2.0) → k3s → workloads. Single-binary CLI.
Repo `getnvoi/core`, binary `bin/nvoi`, module `github.com/getnvoi/core`.

The infra step shells out to a pinned OpenTofu binary auto-downloaded
from `github.com/opentofu/opentofu` releases into the operator's cache
on first use. We left HashiCorp Terraform behind to keep the deploy
pipeline on a permissive licence — the `hashicorp/terraform-exec` and
`hashicorp/terraform-json` libraries we still depend on are MPL-2.0
and CLI-compatible with OpenTofu, so the only real swap was the binary.

## Pipeline (`nvoi deploy`)

```
build               only when any service has build: set
                    preflight (docker info / buildx) → docker login per host
                    → buildx build --push <image>:<deploy-hash>
tf-init / tf-plan -out
detach              only when nodes leaving — drain → etcd member remove →
                    kubectl delete node (for masters; workers skip etcd)
tf-apply <plan>     applies the saved plan, no re-plan
endpoints           parse tofu output (servers, api_endpoint, ha)
openShells          one ssh.Client per server, kept alive through end
installCluster      swap → discover token → primary --cluster-init →
                    secondaries --server → workers via api_endpoint.private
kube-tunnel         kube.New(primaryShell) — apiserver via the same SSH
workloads           registry-auth Secret → Deployments → Services →
                    reconcile (delete nvoi-managed objects no longer in YAML)
defer closeShells   one close per server, end of command
```

## Architecture rules (non-negotiable)

- **ctx is a parameter** end-to-end. Never on a struct.
- **>4 args = struct.** Every function in the tree obeys this. Bundle `(ctx, …)` until you fit; if you can't, you're missing a type.
- **`os.*` lives only at cmd/ boundary**, plus `runtime.Build`, `runner/install.go`, `config.LoadFile`. Internal packages are pure.
- `*config.Config` read-only after `Load`.
- `*runtime.Runtime` is the boot bag: Cfg, Log, SSH keys, CacheDir, WorkDir, DeployHash, Backend, SecretValues. Read-only after `Build`.
- `*deploy.Session` is the **lifecycle bag** (every verb gets one): Rt, Run, Lg + memoized Endpoints + deploy-time accumulators (shells, kc). Methods on `*Session` take only ctx + narrow inputs; rt/run/lg are fields, not args.
- Per-stage handles (Bundle, Runner, ssh.Client, kube.Client) are locals in the lifecycle — never on Runtime.
- One ssh.Client per server per command. The same connection that installs k3s tunnels the kube apiserver.
- Always `--cluster-init` (etcd from day one regardless of master count).

## The Session primitive

`pkg/deploy/Session` is the universal primitive that backs every verb. Built by `deploy.RunWithSession(ctx, rt, kind, fn)` — wraps compile + write-bundle + tf-runner construction (via the package-private `withRunner`), hands `fn` a Session scoped to the given log Kind.

Every cmd/cli verb is a thin cobra adapter over `RunWithSession`:

| Verb | Body |
|---|---|
| `deploy.Run` | RunWithSession + build + tf-init/plan/predrain/apply + installCluster + deployWorkloads |
| `deploy.Destroy` | RunWithSession + tf-init + tf-plan-destroy + tf-destroy |
| `deploy.Plan` | RunWithSession + tf-init + tf-plan |
| `cmd/cli/ssh.go` | `s.OnNode(target, action)` |
| `cmd/cli/exec.go` | `s.OnPrimary(action)` running `install.KubectlExec` |
| `cmd/cli/logs.go` | `s.OnPrimary(action)` running `install.KubectlStream` |
| `cmd/cli/kubectl.go` | `s.OnPrimary(action)` running `install.KubectlStream` |

Methods on `*Session`: `Init` (idempotent tf-init), `Endpoints` (memoized tofu output read), `OnNode(target, action)`, `OnPrimary(action)`, `Sub` via `s.Lg.Sub(kind)`.

## Logging — JSONL canonical, text is a projection

One internal `event` type (unexported — the JSONL schema is the public contract, not the Go shape). Two serializers selected at `log.New(jsonl bool)`. **JSONL is canonical, full-fidelity. Text is a strict projection** — every text line maps 1:1 to a JSONL line; text drops only the `tf:` body.

**Tofu always runs in `-json` internally**, regardless of the operator's `--json` flag. The runner uses the *JSON tfexec variants exclusively (`PlanJSON`/`ApplyJSON`/`DestroyJSON`), pipes events into `Log.TFStream()`, and `tfTransformer` lifts them into normalized Events. The flag only selects rendering (JSONL vs tabbed text) — one code path, two render modes. Earlier the text mode used the human-readable variants, which silently broke the transformer; we don't go back to that.

| Field | Meaning |
|---|---|
| `time` | RFC3339 UTC. Lifted from `@timestamp` for tofu events. |
| `kind` | Closed enum: `infra` \| `build` \| `cluster`. The deploy lifecycle's three buckets. |
| `level` | Closed enum: `step` \| `info` \| `warn` \| `error`. Same field for nvoi events AND lifted tofu events. |
| `step` | populated when `level=step` (stage marker). |
| `msg` | populated when `level≠step`. Lifted from `@message` for tofu events. |
| `tf` | populated for kind=infra wrapped tofu events; renders only in JSONL. |

JSONL example:
```jsonl
{"time":"2026-04-30T15:37:21Z","kind":"infra","level":"step","step":"compile"}
{"time":"2026-04-30T15:37:23Z","kind":"build","level":"info","msg":"#14 [builder 6/6] RUN go build ..."}
{"time":"2026-04-30T15:37:43Z","kind":"infra","level":"info","msg":"OpenTofu 1.11.6","tf":{"type":"version","terraform":"1.11.6"}}
{"time":"2026-04-30T15:37:47Z","kind":"cluster","level":"step","step":"workload-postgres"}
```

Text projection (same data, tabbed):
```
2026-04-30T15:37:21Z	infra	step	compile
2026-04-30T15:37:23Z	build	info	#14 [builder 6/6] RUN go build ...
2026-04-30T15:37:43Z	infra	info	OpenTofu 1.11.6
2026-04-30T15:37:47Z	cluster	step	workload-postgres
```

Pipe-friendly: `cut -f3` = level, `cut -f4` = payload. Embedded tabs in payload are sanitized to spaces so column count is invariant.

`log.Log.Sub(kind)` returns a kind-scoped logger. The orchestration layer (`pkg/deploy/`) scopes per phase: `rt.Log.Sub(KindBuild)` for build, `Sub(KindInfra)` for tofu ops, `Sub(KindCluster)` for k3s/kube/caddy.

Tofu's native `-json` events are normalized at `Log.TFStream()`: `@level → level`, `@message → msg`, `@timestamp → time`, `@module` dropped, the rest folded under `tf:`. Non-JSON lines (tofu's pretty-printed `output -json` dump) are silently filtered. **NOTHING in the codebase writes to os.Stdout or os.Stderr directly.**

## Layout

Three roots with strict visibility rules:

- `pkg/*` — public Go library (the API third parties may import).
- `pkg/internal/*` — library-private. Importable from anywhere under `pkg/`, invisible to outside consumers (Go's `internal/` rule).
- `internal/cli/` — CLI-only glue. Importable from `cmd/*`, invisible to library consumers.

```
cmd/cli/                 main, root, deploy, destroy, plan, ssh, kubectl,
                         exec, logs — all thin cobra adapters over pkg/deploy

internal/
  cli/                   CLI-only orchestration: load.go (PrepareRuntime),
                         dotenv.go (LoadDotEnv / ResolveDotEnv),
                         inputs.go (LoadConfig / ConfigureState /
                         ResolveBucketCreds / ResolveProviderInputs),
                         secrets.go (ResolveSecrets), sshkey.go (ReadSSHKeys
                         + ~ expansion), aliases.go (ExpandAliasArgs ->
                         delegates to config.ExpandAlias)

pkg/                     PUBLIC SURFACE (10 packages):
  config/                Config + LoadFile + ParseYAML + Validate;
                         ExpandAlias (alias-body tokenization);
                         types: Providers, ServerSpec, ServiceSpec,
                         BuildSpec, RegistryDef, Domains, StorageSpec
  runtime/               Inputs + Build → *Runtime (boot bag)
  deploy/                lifecycle layer above install/workload + the
                         pkg/internal/* packages: deploy.go (Run),
                         destroy.go, plan.go, lifecycle.go (private
                         withRunner), session.go (Session + RunWithSession
                         + OnNode/OnPrimary/Endpoints/Init), install.go
                         (installCluster), workloads.go, predrain.go,
                         shells.go
  install/               wait, swap, k3s.go (constants + Node —
                         bundles Shell+Log), discover, primary
                         (InstallPrimaryMaster), secondary
                         (JoinSecondaryMaster + SecondaryJoinSpec),
                         worker (JoinWorker + WorkerJoinSpec), ready,
                         kubectl (Kubectl/KubectlStream/KubectlExec +
                         KubectlSpec)
  workload/              deployment.go / statefulset.go / service.go /
                         secret.go / registry.go / reconcile.go /
                         placement.go / naming.go.
                         Public: ApplyAll, ResolveRegistryCreds,
                         LabelNvoiRole. All builders + reconcile
                         primitives are package-internal.
  ssh/                   *Client + Shell interface (Run / RunStream /
                         Addr / DialTCP)
  log/                   Log interface; Kind enum + KindBuild/Infra/
                         Cluster constants; New / NewWith. Internal
                         event/level types — JSONL schema is the public
                         contract.
  state/                 Configure(ctx, *cfg, bp, lg): ensure state bucket;
                         Backend struct
  providers/
    bucket.go            BucketProvider interface
    registry.go          RegisterBucket / ResolveBucket / IsRegisteredBucket /
                         BucketFactory / CredentialSchema(ForBucket)
    reserved.go          RegisterReservedServerNames / ReservedServerNames
    cloudflare/          R2 BucketProvider + DNS emitter
    hetzner/             InfraEmitter (compile.go, register.go, hetzner.tf.tmpl)
  naming/                Prefix, Server, StateBucket, WorkDir, ProviderHCL,
                         CacheDirSegment, DefaultUser

  internal/              LIBRARY-PRIVATE (importable only from pkg/*):
    build/               Runner interface; DockerRunner; Plan / HostsToPush /
                         All — fail-fast pipeline
    cloudinit/           cloud-init renderer (shared by infra emitters)
    compile/             cfg → []byte HCL via registered InfraEmitter +
                         RegisterDNS
    detach/              Nodes (drain + etcd remove + kubectl delete) +
                         etcd.go
    kube/                client.go (SSH-tunneled apiserver) + apply.go +
                         owned.go (Scope + ApplyOwned/ListOwned/SweepOwned)
                         + caddy.go + caddy_config.go + caddy_manifests.go
                         + exec.go + labels.go
    runner/              tfexec wrapper driving an OpenTofu binary;
                         plan.go (PlanWithOut, ApplyPlan,
                         PlanDestroyWithOut), destroys.go, endpoints.go,
                         runner.go, install.go (EnsureTofu — fetches
                         OpenTofu releases, sha256-verified)
    utils/               httpclient (Request struct + Do(ctx, req)) +
                         ShellQuote / ShellSplit / SortedKeys / ResolveVar
    testutil/
      hcltest/           ParseValid / FindBlock / StringAttr
      sshfake/           canned-response Shell, substring matchers
```

## Run

```
bin/nvoi deploy   -c examples/minimal.yaml
bin/nvoi plan     -c examples/minimal.yaml
bin/nvoi destroy  -c examples/minimal.yaml
bin/nvoi ssh      [target] -- <cmd>
bin/nvoi kubectl  -- <args>
bin/nvoi exec     <service> -- <cmd> [args]
bin/nvoi logs     <service> [-f] [--tail N] [--since DUR]
bin/nvoi <alias>  # operator-defined shortcut from aliases:

bin/deploy --json    # JSONL canonical schema on stdout
bin/deploy           # text projection on stderr (tabbed values)
```

`.env` (gitignored) loaded natively at startup. Required: `HCLOUD_TOKEN`. When
`providers.storage: cloudflare`: `CF_API_KEY`, `CF_ACCOUNT_ID`. `$VAR` in
`registry:` resolves against env at the boundary.

## State

- Default: local at `.tf/<app>-<env>/terraform.tfstate` (filename retained
  by OpenTofu for state-format compatibility).
- `providers.storage: cloudflare` → bucket `nvoi-{app}-{env}-tfstate` auto-created on R2,
  tofu `s3` backend injected with creds. Auto-migrates both directions.

## Conventions

- `app` + `env` are the namespace. Names: `nvoi-{app}-{env}-{resource}`.
- Every server in YAML = a k3s node. Reserved YAML keys are per-provider — registered via `providers.RegisterReservedServerNames(provider, set)` in each emitter's init(). Hetzner reserves `default`, `cp`.
- HA emerges automatically from N≥2 masters (LB auto-emitted, label-selector targets).
  Exactly one master must have `primary: true` when N≥2; implicit for N=1.
- Workloads tagged via `kube.Scope{Namespace, Owner}`. `kube.ApplyOwned` stamps `nvoi/owner=<owner>`; `kube.SweepOwned` filters by it. Owner taxonomy: `services` / `registry` / `app-secrets` / `caddy`.
- `registry:` block is the ONE source of truth for credentials — same creds drive
  push (operator's docker daemon, via `docker login --password-stdin`) AND pull
  (cluster's `imagePullSecret`).
- Workload images get `<image>:<deploy-hash>` only when `build:` is set.
  Pre-built images stay verbatim.

## Tests

`bin/test` → `go test -timeout 30s ./...`. Covered: config, compile, runner
(plan-walker), log, detach, install (cmd construction), state, providers/cloudflare,
build, workload, kube (apply + owned + caddy), internal/cli (dotenv, secrets,
sshkey, inputs, alias expansion via cmd/cli). Untested by design: `kube` tunnel,
`runner` tfexec wrapper, `cmd/cli` cobra wiring (verb dispatch only — verb
bodies live in pkg/deploy and internal/cli, both covered) — validated by live deploys.

## Add a provider

1. `pkg/providers/<name>/compile.go` — `EmitInfra(rt) ([]byte, error)` and `ServerResourceType() string`
2. `pkg/providers/<name>/register.go` — `compile.RegisterInfra` in `init()` (imports `github.com/getnvoi/core/pkg/internal/compile`). Optionally `providers.RegisterReservedServerNames(name, ...)` for YAML keys that collide with non-server resources.
3. Blank-import in `cmd/cli/main.go`

For a bucket provider:

1. `pkg/providers/<name>/bucket.go` — implements `providers.BucketProvider`
2. `register.go` — `providers.RegisterBucket(name, schema, factory)` in `init()`
3. Blank-import in `cmd/cli/main.go`

Note: `pkg/internal/compile` is library-private. Third-party packages outside `pkg/` cannot register providers — providers must live under `pkg/providers/<name>/` (vendored or forked) to reach the registry.
