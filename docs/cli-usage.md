# CLI Usage

## Config

By default, `nvoi` reads `./nvoi.yaml`.

Use another file with:

```bash
nvoi --config path/to/config.yaml deploy
```

## Environment

The CLI is responsible for local machine concerns:
- loading `.env`
- resolving secret env vars
- resolving registry env vars
- expanding `~` in `ssh_key`
- reading SSH key files

`.env` is loaded from:
- the current working directory first
- otherwise the directory containing the config file

## Commands

Deploy:

```bash
nvoi deploy
```

Plan:

```bash
nvoi plan
```

Destroy:

```bash
nvoi destroy
```

Stream JSON logs:

```bash
nvoi --json deploy
```

## Aliases

Aliases come from `aliases:` in `nvoi.yaml`.

If this exists:

```yaml
aliases:
  weblogs: logs web -f
```

then this works:

```bash
nvoi weblogs
```
