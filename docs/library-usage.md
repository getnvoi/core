# Library Usage

## Stability

Pre-1.0. **Any 0.x release may introduce breaking changes** to package
layout, exported types, and function signatures. Pin a specific commit
or tag in your `go.mod` if you depend on `github.com/getnvoi/core/pkg/...`:

```
require github.com/getnvoi/core v0.0.0-YYYYMMDDHHMMSS-<commit>
```

A stability promise will be documented when v1 lands. Until then, the
public surface is the set of packages directly under `pkg/` (excluding
`pkg/internal/`) — anything imported from `pkg/internal/` is not part
of the API and the Go compiler will refuse the import anyway.

## Contract

`nvoi` library packages do not read files, env vars, home directories, or cwd.
They only consume explicit inputs.

The CLI remains responsible for:
- loading YAML
- loading `.env`
- resolving `os.Getenv`
- expanding `~`
- reading SSH key files
- choosing cache and work dirs

## Public API

### Config model

```go
cfg, err := config.ParseYAML(data)
if err != nil {
	return err
}
if err := cfg.Validate(); err != nil {
	return err
}
```

`config.Config` is the declarative deploy model.
YAML is only one adapter into that model.

### Runtime inputs

```go
rt, err := runtime.Build(ctx, runtime.Inputs{
	Config: cfg,
	Log:    lg,
	Paths: runtime.Paths{
		WorkDir:  workDir,
		CacheDir: cacheDir,
	},
	SSH: runtime.SSHMaterial{
		PublicKey:  pubKey,
		PrivateKey: privKey,
	},
	Secrets:       secretValues,
	RegistryCreds: registryCreds,
	StateBackend:  backend,
	Providers:     providerInputs,
	DeployHash:    deployHash,
})
if err != nil {
	return err
}
```

All runtime dependencies must be provided explicitly here.

### Orchestration

```go
if err := deploy.Run(ctx, rt); err != nil {
	return err
}
```

This is the main entrypoint.

## Example

```go
cfg, err := config.ParseYAML(yamlBytes)
if err != nil {
	return err
}
if err := cfg.Validate(); err != nil {
	return err
}

rt, err := runtime.Build(ctx, runtime.Inputs{
	Config: cfg,
	Log:    lg,
	Paths: runtime.Paths{
		WorkDir:  "/tmp/nvoi-app",
		CacheDir: "/tmp/nvoi-cache",
	},
	SSH: runtime.SSHMaterial{
		PublicKey:  pubKey,
		PrivateKey: privKey,
	},
	Secrets: map[string]string{
		"DATABASE_URL": "...",
	},
	RegistryCreds: map[string]config.RegistryDef{
		"ghcr.io": {Username: "u", Password: "p"},
	},
	Providers: runtime.ProviderInputs{
		Cloudflare: &runtime.CloudflareInputs{
			APIToken:  "...",
			AccountID: "...",
			ZoneID:    "...",
			Zone:      "example.com",
		},
		Hetzner: &runtime.HetznerInputs{
			Token: "...",
		},
	},
	DeployHash: "20260501-120000",
})
if err != nil {
	return err
}

return deploy.Run(ctx, rt)
```
