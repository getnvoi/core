# CLAUDE.md — nvoi

YAML → tofu (OpenTofu, MPL-2.0) → k3s → workloads. Single-binary CLI.
Repo `getnvoi/core`, binary `bin/nvoi`, module `github.com/getnvoi/core`.

nvoi is a **substrate**, not a product. It provisions compute, installs
k3s, exposes the cluster via a Cloudflare tunnel, allocates S3-compatible
storage, and deploys workloads declared in YAML. Anything above that
(observability, application bundles, operator UIs) gets built on top.
The substrate's job is to do a small number of things excellently and
refuse to be opinionated about everything else.

The infra step shells out to a pinned OpenTofu binary auto-downloaded
from `github.com/opentofu/opentofu` releases into the operator's cache
on first use. We left HashiCorp Terraform behind to keep the deploy
pipeline on a permissive licence — the `hashicorp/terraform-exec` and
`hashicorp/terraform-json` libraries we still depend on are MPL-2.0
and CLI-compatible with OpenTofu, so the only real swap was the binary.

## Topology

**All-tunnel.** Public network surface = SSH (22) only. HTTP/S ingress
enters via Cloudflare's edge, terminates at CF, rides an outbound
cloudflared tunnel into the cluster, lands on the in-cluster Traefik
Service (k3s's default ingress controller), and routes by Host header
to the workload's ClusterIP — per-request L7 load balancing via the
EndpointSlice watch.

Cloudflare is a **hard dependency**: DNS, tunnel, and (optionally) the
state bucket all live there. There's no DNS or ingress abstraction —
`providers.dns` / `providers.ingress` don't exist. CF tunnel activates
implicitly whenever `domains:` is non-empty.

**Apiserver HA = opt-in via top-level `ha: true`.** Hetzner Cloud's
private network is software-defined and L3-routed — packets only
flow to IPs registered to specific server NICs via Hetzner's API.
That rules out kube-vip / ARP-mode VIPs (the router black-holes
self-assigned addresses). The canonical k3s-on-Hetzner HA pattern,
which we ship: a private-only hcloud LB on port 6443 fronting the
master pool. Workers + secondary masters dial the LB private IP;
LB health-checks each master and steers around dead ones in ~10s.

Validator rules:
- `ha: false` (or unset, default): exactly 1 master required. Workers
  point at the lone master's private IP. Sub-90s rebuild from R2
  tfstate on hardware failure. The 90% case.
- `ha: true`: odd master count ≥3 (etcd quorum). Emits the LB.
  Costs ~5€/month per cluster (lb11). The production path.

Going one direction or the other is a single deploy — `ha:` and
master count change atomically. The substrate destroys / creates
exactly the right set of resources (LB + extra masters) in one
tf-apply.

## Pipeline (`nvoi deploy`)

```
build               only when any service has build: set
                    preflight (docker info / buildx) → docker login per host
                    → buildx build --push <image>:<deploy-hash>
tf-init / tf-plan -out
detach              only when nodes leaving — drain → etcd member remove →
                    kubectl delete node (for masters; workers skip etcd)
tf-apply <plan>     applies the saved plan, no re-plan
endpoints           parse tofu output (servers, api_endpoint, ha, tunnel)
openShells          one ssh.Client per server, kept alive through end
installCluster      swap → discover token →
                    primary --cluster-init →
                    secondaries --server <api_endpoint.private> →
                    workers --server <api_endpoint.private>
                    (api_endpoint.private = LB private IP when ha: true,
                     else primary master's private IP)
kube-tunnel         kube.New(primaryShell) — apiserver via the same SSH
workloads           registry-auth Secret → Deployments → Services →
                    Ingresses (no TLS section; CF terminates at edge) →
                    cloudflared Deployment (when domains: set) →
                    reconcile (delete nvoi-managed objects no longer in YAML)
defer closeShells   one close per server, end of command
```

## Architecture rules (non-negotiable)

- **ctx is a parameter** end-to-end. Never on a struct.
- **>4 args = struct.** Every function in the tree obeys this. Bundle `(ctx, …)` until you fit; if you can't, you're missing a type.
- **`os.*` lives only at cmd/ boundary**, plus `runtime.Build`, `runner/install.go`, `config.LoadFile`. Internal packages are pure.
- `*config.Config` read-only after `Load`.
- `*runtime.Runtime` is the boot bag: Cfg, Log, SSH keys, CacheDir, WorkDir, DeployHash, Backend, SecretValues, Providers. Read-only after `Build`.
- `*deploy.Session` is the **lifecycle bag** (every verb gets one): Rt, Run, Lg + memoized Endpoints + deploy-time accumulators (shells, kc). Methods on `*Session` take only ctx + narrow inputs; rt/run/lg are fields, not args.
- Per-stage handles (Bundle, Runner, ssh.Client, kube.Client) are locals in the lifecycle — never on Runtime.
- One ssh.Client per server per command. The same connection that installs k3s tunnels the kube apiserver.
- Always `--cluster-init` (etcd from day one regardless of master count).
- Manifest builders are **typed** (real `*appsv1.Deployment`, `*corev1.Secret`, etc.) — no YAML string-templating.

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

**Tofu always runs in `-json` internally**, regardless of the operator's `--json` flag. The runner uses the *JSON tfexec variants exclusively (`PlanJSON`/`ApplyJSON`/`DestroyJSON`), pipes events into `Log.TFStream()`, and `tfTransformer` lifts them into normalized Events. The flag only selects rendering (JSONL vs tabbed text) — one code path, two render modes.

| Field | Meaning |
|---|---|
| `time` | RFC3339 UTC. Lifted from `@timestamp` for tofu events. |
| `kind` | Closed enum: `infra` \| `build` \| `cluster`. The deploy lifecycle's three buckets. |
| `level` | Closed enum: `step` \| `info` \| `warn` \| `error`. Same field for nvoi events AND lifted tofu events. |
| `step` | populated when `level=step` (stage marker). |
| `msg` | populated when `level≠step`. Lifted from `@message` for tofu events. |
| `tf` | populated for kind=infra wrapped tofu events; renders only in JSONL. |

`log.Log.Sub(kind)` returns a kind-scoped logger. The orchestration layer (`pkg/deploy/`) scopes per phase: `rt.Log.Sub(KindBuild)` for build, `Sub(KindInfra)` for tofu ops, `Sub(KindCluster)` for k3s/kube/cloudflared/ingress.

**NOTHING in the codebase writes to os.Stdout or os.Stderr directly.**

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

pkg/                     PUBLIC SURFACE:
  config/                Config + LoadFile + ParseYAML + Validate;
                         ExpandAlias (alias-body tokenization);
                         types: Providers, ServerSpec, ServiceSpec,
                         BuildSpec, RegistryDef, Domains, StorageSpec.
                         Top-level `ha: bool` opts into apiserver HA;
                         validator enforces (ha: false → 1 master),
                         (ha: true → odd master count ≥3).
  runtime/               Inputs + Build → *Runtime (boot bag)
  deploy/                lifecycle layer above install/workload + the
                         pkg/internal/* packages: deploy.go (Run),
                         destroy.go, plan.go, lifecycle.go (private
                         withRunner), session.go (Session + RunWithSession
                         + OnNode/OnPrimary/Endpoints/Init), install.go
                         (installCluster), workloads.go (cloudflared
                         apply/sweep), predrain.go, shells.go
  install/               wait, swap, k3s.go (constants + Node —
                         bundles Shell+Log), discover, primary
                         (InstallPrimaryMaster), secondary
                         (JoinSecondaryMaster + SecondaryJoinSpec),
                         worker (JoinWorker + WorkerJoinSpec), ready,
                         kubectl (Kubectl/KubectlStream/KubectlExec)
  workload/              deployment.go / statefulset.go / service.go /
                         secret.go / registry.go / reconcile.go /
                         ingress.go (no TLS — CF terminates at edge) /
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
    bucket.go            BucketProvider interface (S3/R2/Scaleway/MinIO)
    registry.go          RegisterBucket / ResolveBucket / IsRegisteredBucket /
                         BucketFactory / CredentialSchema(ForBucket)
    reserved.go          RegisterReservedServerNames / ReservedServerNames
    cloudflare/          R2 BucketProvider (bucket.go) + CF tunnel emitter
                         (dns.go, EmitTunnelDNS — called directly by compile,
                         no DNSEmitter interface)
    hetzner/             InfraEmitter (compile.go, register.go, hetzner.tf.tmpl)
  naming/                Prefix, Server, StateBucket, WorkDir, ProviderHCL,
                         CacheDirSegment, DefaultUser, TraefikInClusterURL

  internal/              LIBRARY-PRIVATE (importable only from pkg/*):
    build/               Runner interface; DockerRunner; Plan / HostsToPush /
                         All — fail-fast pipeline
    cloudinit/           cloud-init renderer (shared by infra emitters)
    compile/             cfg → []byte HCL via registered InfraEmitter +
                         direct CF tunnel emission (no DNS interface)
    detach/              Nodes (drain + etcd remove + kubectl delete) +
                         etcd.go
    kube/                client.go (SSH-tunneled apiserver) + apply.go
                         (per-kind Get-then-Update helpers) + owned.go
                         (Scope + ApplyOwned/ListOwned/SweepOwned) +
                         tunnel.go (cloudflared Deployment apply/sweep) +
                         exec.go + labels.go
    runner/              tfexec wrapper driving an OpenTofu binary;
                         plan.go (PlanWithOut, ApplyPlan,
                         PlanDestroyWithOut), destroys.go, endpoints.go
                         (Servers + APIEndpoint + Tunnel outputs),
                         runner.go, install.go (EnsureTofu)
    utils/               httpclient (Request struct + Do(ctx, req)) +
                         ShellQuote / ShellSplit / SortedKeys / ResolveVar
    testutil/
      hcltest/           ParseValid / FindBlock / StringAttr
      sshfake/           canned-response Shell, substring matchers
```

## YAML surface

Single-master (default):

```yaml
app: hello
env: dev

providers:
  infra: hetzner       # required — InfraEmitter registry lookup
  storage: cloudflare  # optional — BucketProvider; empty = local state

ssh_key: ~/.ssh/id_rsa.pub

servers:
  master: { type: cax21, region: nbg1, role: master }
  worker-1: { type: cax11, region: nbg1, role: worker }  # optional

services:
  whoami:
    image: traefik/whoami
    port: 80

domains:
  whoami: [whoami.nvoi.to]
```

Apiserver HA (opt-in):

```yaml
ha: true   # triggers private hcloud LB on :6443; validator requires odd masters ≥3

servers:
  master-1: { type: cax21, region: nbg1, role: master, primary: true }
  master-2: { type: cax21, region: nbg1, role: master }
  master-3: { type: cax21, region: nbg1, role: master }
  worker-1: { type: cax11, region: nbg1, role: worker }
```

Optional blocks: `registry:`, `secrets:`, `aliases:`, per-service `build:` / `storage:` / `servers:` / `replicas:` / `secrets:`.

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

bin/nvoi deploy --json    # JSONL canonical schema on stdout
bin/nvoi deploy           # text projection on stderr (tabbed values)
```

`.env` (gitignored) loaded natively at startup. Required env vars:

| Var | When | Purpose |
|---|---|---|
| `HCLOUD_TOKEN` | always (hetzner) | Hetzner API token |
| `CF_API_KEY` | `providers.storage: cloudflare` or `domains:` | CF API token (R2 + DNS + tunnel) |
| `CF_ACCOUNT_ID` | `providers.storage: cloudflare` or `domains:` | CF account ID |
| `CF_ZONE_ID` | `domains:` | CF zone ID for the domain's zone |
| `CF_ZONE` | `domains:` | CF zone literal (e.g. "nvoi.to") |
| `CF_TUNNEL_SECRET` | `domains:` | operator-supplied 32-byte base64 string (rotation = regenerate + redeploy) |

`$VAR` in `registry:`, `secrets:` (per-service) resolves against env at the cmd/cli boundary.

## State

- Default: local at `.tf/<app>-<env>/terraform.tfstate` (filename retained by OpenTofu for state-format compatibility).
- `providers.storage: cloudflare` → bucket `nvoi-{app}-{env}-tfstate` auto-created on R2, tofu `s3` backend injected with creds. Auto-migrates both directions.

## Conventions

- `app` + `env` are the namespace. Names: `nvoi-{app}-{env}-{resource}`.
- Every server in YAML = a k3s node. Reserved YAML keys are per-provider — registered via `providers.RegisterReservedServerNames(provider, set)` in each emitter's init(). Hetzner reserves `default` (network/firewall/subnet).
- HA is opt-in via top-level `ha: true`. When set, validator requires an odd master count ≥3 and emits a private hcloud LB on :6443 (`api_endpoint.private`). Exactly one master must have `primary: true` when N≥2; implicit for N=1. Default (`ha: false` or unset) is single master + N workers.
- Workloads tagged via `kube.Scope{Namespace, Owner}`. `kube.ApplyOwned` stamps `nvoi/owner=<owner>`; `kube.SweepOwned` filters by it. Owner taxonomy: `services` / `registry` / `app-secrets` / `ingress` / `tunnel`.
- Kinds supported by List/Sweep: `Deployment` / `StatefulSet` / `Service` / `Secret` / `Ingress`. `Namespace` is applied (tunnel namespace) but never swept — empty namespaces are cheap and serve as debugging breadcrumbs.
- `registry:` block is the ONE source of truth for credentials — same creds drive push (operator's docker daemon, via `docker login --password-stdin`) AND pull (cluster's `imagePullSecret`).
- Workload images get `<image>:<deploy-hash>` only when `build:` is set. Pre-built images stay verbatim.

## Tests

`bin/test` → `go test -timeout 30s ./...`. Covered: config, compile, runner (plan-walker), log, detach, install (cmd construction), state, providers/cloudflare, providers/hetzner, build, workload, kube (apply + owned), internal/cli (dotenv, secrets, sshkey, inputs, alias expansion via cmd/cli). Untested by design: `kube` tunnel, `runner` tfexec wrapper, `cmd/cli` cobra wiring (verb dispatch only — verb bodies live in pkg/deploy and internal/cli, both covered) — validated by live deploys.

## Add an infra provider

1. `pkg/providers/<name>/compile.go` — `EmitInfra(rt) ([]byte, error)` and `ServerResourceType() string`
2. `pkg/providers/<name>/register.go` — `compile.RegisterInfra` in `init()` (imports `github.com/getnvoi/core/pkg/internal/compile`). Optionally `providers.RegisterReservedServerNames(name, ...)` for YAML keys that collide with non-server resources.
3. Blank-import in `cmd/cli/root.go`

For a bucket provider:

1. `pkg/providers/<name>/bucket.go` — implements `providers.BucketProvider`
2. `register.go` — `providers.RegisterBucket(name, schema, factory)` in `init()`
3. Blank-import in `cmd/cli/root.go`

DNS is not pluggable — Cloudflare is the only DNS+tunnel provider (substrate-level dependency). Compile calls `cloudflare.EmitTunnelDNS` directly.

Note: `pkg/internal/compile` is library-private. Third-party packages outside `pkg/` cannot register providers — providers must live under `pkg/providers/<name>/` (vendored or forked) to reach the registry.
