# CLAUDE.md — nvoi

YAML → terraform → k3s → workloads. Single-binary CLI. Repo `getnvoi/core`,
binary `bin/nvoi`, module `github.com/getnvoi/core`.

## Pipeline (`nvoi deploy`)

```
build               only when any service has build: set
                    preflight (docker info / buildx) → docker login per host
                    → buildx build --push <image>:<deploy-hash>
tf-init / tf-plan -out
detach              only when nodes leaving — drain → etcd member remove →
                    kubectl delete node (for masters; workers skip etcd)
tf-apply <plan>     applies the saved plan, no re-plan
endpoints           parse terraform output (servers, api_endpoint, ha)
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
- **`os.*` lives only at cmd/ boundary**, plus `runtime.Build`, `runner/install.go`, `config.Load`. Internal packages are pure.
- `*config.Config` read-only after `Load`.
- `*runtime.Runtime` is the bag: Cfg, Log, SSH keys, CacheDir, WorkDir, DeployHash, Backend. Read-only after `Build`.
- Per-stage handles (Bundle, Runner, ssh.Client, kube.Client) are locals in the lifecycle — never on Runtime.
- One ssh.Client per server per command. The same connection that installs k3s tunnels the kube apiserver.
- Always `--cluster-init` (etcd from day one regardless of master count).

## Layout

```
cmd/cli/                 main, deploy, plan, destroy, ssh, kubectl, sshkey, dotenv
internal/
  config/                Config + Load + Validate (validate.go)
                         types: Providers, ServerSpec, ServiceSpec, BuildSpec, RegistryDef
  runtime/               Inputs + Build → *Runtime
  compile/               cfg → []byte HCL via registered InfraEmitter
  cloudinit/             cloud-init renderer (shared)
  providers/
    bucket.go            BucketProvider interface
    registry.go          RegisterBucket / ResolveBucket
    cloudflare/          R2 BucketProvider (api.go, bucket.go, register.go)
    hetzner/             InfraEmitter (compile.go, register.go, templates/hetzner.tf.tmpl)
  state/                 Configure: ensure state bucket + return Backend for HCL
  runner/                tfexec wrapper; PlanWithOut, ApplyPlan, Endpoints,
                         PlannedNodeDestroys, EnsureTerraform
  ssh/                   *Client + Shell interface (Run / RunStream / Addr)
  install/               wait, swap, k3s.go (constants),
                         discover, primary, secondary, worker, ready, kubectl
  detach/                Nodes (drain + etcd remove + kubectl delete) + etcd.go
  kube/                  client.go (SSH-tunneled apiserver) + apply.go
                         Apply{Deployment,Service,Secret} / Delete*  / List nvoi-owned
  build/                 Runner interface; DockerRunner (info/buildx/login/build)
                         Plan, HostsToPush, All — fail-fast pipeline
  workload/              deployment.go / service.go / registry.go / reconcile.go
                         BuildDeployment / BuildService / BuildRegistrySecret
                         ApplyAll + ReconcileRemoval
  log/                   Log interface; text + jsonl; Stream + TFStream
  utils/                 httpclient (shared JSON-over-HTTP)
  naming/                pure string helpers
  testutil/
    hcltest/             ParseValid / FindBlock / StringAttr
    sshfake/             canned-response Shell, substring matchers
```

## Run

```
bin/nvoi deploy   -c examples/minimal.yaml
bin/nvoi plan     -c examples/minimal.yaml
bin/nvoi destroy  -c examples/minimal.yaml
bin/nvoi ssh      [target] -- <cmd>
bin/nvoi kubectl  -- <args>
```

`.env` (gitignored) loaded natively at startup. Required: `HCLOUD_TOKEN`. When
`providers.storage: cloudflare`: `CF_API_KEY`, `CF_ACCOUNT_ID`. `$VAR` in
`registry:` resolves against env at the boundary.

## State

- Default: local at `.tf/<app>-<env>/terraform.tfstate`.
- `providers.storage: cloudflare` → bucket `nvoi-{app}-{env}-tfstate` auto-created on R2,
  terraform `s3` backend injected with creds. Auto-migrates both directions.

## Conventions

- `app` + `env` are the namespace. Names: `nvoi-{app}-{env}-{resource}`.
- Every server in YAML = a k3s node. Reserved YAML keys: `default`, `cp`.
- HA emerges automatically from N≥2 masters (LB auto-emitted, label-selector targets).
  Exactly one master must have `primary: true` when N≥2; implicit for N=1.
- Workloads tagged `nvoi/owner=nvoi`, `nvoi/service=<name>`, `nvoi/deploy-hash=<hash>`.
  Reconcile-on-removal filters by `nvoi/owner=nvoi`; unmanaged objects are untouched.
- `registry:` block is the ONE source of truth for credentials — same creds drive
  push (operator's docker daemon, via `docker login --password-stdin`) AND pull
  (cluster's `imagePullSecret`).
- Workload images get `<image>:<deploy-hash>` only when `build:` is set.
  Pre-built images stay verbatim.

## Tests

`bin/test` → `go test -timeout 30s ./...`. Covered: config, compile, runner
(plan-walker), log, detach, install (cmd construction), state, providers/cloudflare,
build, workload. Untested by design: `kube` tunnel, `runner` tfexec wrapper,
`cmd/cli` orchestration — validated by live deploys.

## Add a provider

1. `internal/providers/<name>/compile.go` — `EmitInfra(rt) ([]byte, error)` and `ServerResourceType() string`
2. `internal/providers/<name>/register.go` — `compile.RegisterInfra` in `init()`
3. Blank-import in `cmd/cli/main.go`

For a bucket provider:

1. `internal/providers/<name>/bucket.go` — implements `providers.BucketProvider`
2. `register.go` — `providers.RegisterBucket(name, schema, factory)` in `init()`
3. Blank-import in `cmd/cli/main.go`
