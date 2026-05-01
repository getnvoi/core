# Using Examples

## Minimal

[`examples/minimal.yaml`](/Users/ben/Desktop/nvoi/tf/examples/minimal.yaml:1) is the smallest working shape:

```bash
nvoi --config examples/minimal.yaml deploy
```

It defines:
- one master node
- Hetzner infra
- Cloudflare-backed state storage

## HA

[`examples/ha.yaml`](/Users/ben/Desktop/nvoi/tf/examples/ha.yaml:1) shows a multi-node cluster:

```bash
nvoi --config examples/ha.yaml deploy
```

It defines:
- three masters
- two workers
- one primary master

## Local project config

[`nvoi.yaml`](/Users/ben/Desktop/nvoi/tf/nvoi.yaml:1) is the full example in this repo.

It includes:
- services
- registry credentials
- secrets
- domains
- tunnel and DNS providers
- aliases

Use it the same way:

```bash
nvoi deploy
```
