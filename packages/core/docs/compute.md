# compute

Where the core's machines, clusters and databases come from, each behind a behaviour; the core (`packages/core`)
names no provider. One namespace (`Compute`) inside the core's app, configured under `config :nvoi, Compute`.

## Machines

`Compute.SandboxVm` is the behaviour a sandbox provider implements (Daytona today, `Compute.SandboxVm.Daytona`):
make, wake, stop, copy and remove a machine. `Compute.Box` reaches `boxd` on it.
`config :nvoi, Compute, providers: [{"daytona", Compute.SandboxVm.Daytona}]`; their keys come from the `credentials`
function configured, prices from the `price_table` module configured.

## Clusters

Managed Kubernetes for deploying apps to production: a k3s cluster (one master, N nodes on a private network,
SSH the only port open) with volumes, made and kept on a provider's VMs. `Compute.Cluster` is the API;
`Compute.Cluster.Provider` is the behaviour a provider implements (Hetzner today, `Compute.Cluster.Hetzner`).
Machines exist only as the cluster's master and nodes; kubectl runs on the master over SSH.

## Databases

Managed databases for environments: a root database per environment and a branch per worktree, on Neon (Postgres)
and Turso (SQLite) behind `Compute.Database`. `config :nvoi, Compute, database_vendors: [{"neon", Compute.Database.Neon}, ...]`.

A production environment's database is a root of its own, never branched: `find` takes it back by its name when a
make was cut before its ref was kept, `size` reads its main branch's bytes. Both are optional callbacks; Neon has them.
