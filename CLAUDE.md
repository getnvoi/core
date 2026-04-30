# CLAUDE.md — nvoi-tf

YAML → HCL → Terraform builder. Sibling-track validation that nvoi's YAML
surface compiles to a Terraform pack and lets TF own the resource graph,
state, and drift detection. Goal: drop most of `../nvoi/` once coverage
matches.

## Layout

```
cmd/cli/                 Cobra entrypoint, one file per command
internal/
  config/                typed YAML loader + validation
  compile/               cfg → HCL Bundle (registry-based dispatch)
  cloudinit/             user-data renderer (shared across IaaS providers)
  runner/                embedded `terraform` (hc-install) + tfexec wrappers
  naming/                deterministic `nvoi-{app}-{env}-*` helpers
  providers/<name>/      per-provider HCL emitter; init() registers
                         with the compile registry
examples/                minimal YAML samples
bin/tf                   entrypoint — sources .env, runs cmd/cli
```

State backend is **local** — `terraform.tfstate` lives in
`.tf/<app>-<env>/`. Remote-state on object storage will land in a future
`internal/state/` package once we have an API path to provision the
bucket creds programmatically.

## Add a provider

1. `internal/providers/<name>/compile.go` — `EmitInfra(b, cfg) error`
2. `internal/providers/<name>/register.go` — `compile.RegisterInfra` in `init()`
3. Blank-import in `cmd/cli/main.go`

## Run

```
bin/tf deploy  -c examples/minimal.yaml
bin/tf plan    -c examples/minimal.yaml
bin/tf destroy -c examples/minimal.yaml
```

Hetzner env:

- `HCLOUD_TOKEN` — Hetzner Cloud API token (TF provider)

SSH public key path is declared in YAML (`ssh_key:` field). No env fallback,
no implicit search path — set it explicitly.

State bucket: `nvoi-{app}-{env}-tfstate` on the configured infra
provider's object storage. Auto-created idempotently before `terraform init`.
