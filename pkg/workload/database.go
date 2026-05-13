package workload

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/runtime"
)

// databaseEnvVars injects the 5 canonical env vars per database
// binding declared in svc.Databases. Each entry expands to:
//
//	<PREFIX>_URL       ← credentials Secret key `url`
//	<PREFIX>_HOST      ← `host`
//	<PREFIX>_PORT      ← `port`
//	<PREFIX>_USER      ← `user`
//	<PREFIX>_PASSWORD  ← `password`
//
// SecretKeyRef (NOT envFrom) — envFrom doesn't uppercase the
// credentials Secret's lowercase keys, so envFrom-prefix would
// produce `<PREFIX>_url` etc. and apps' `os.Getenv("DATABASE_URL")`
// would fail at runtime. Same discipline as the cmd/db image's
// DB_* binding (see providers.dbCredsEnv).
//
// Validator (pkg/config) has already rejected:
//   - bindings referencing undeclared databases
//   - duplicate prefixes within one service
//   - prefixes that collide with the canonical suffixes
//     (`DATABASE_URL=app` foot-gun)
//
// so we can resolve config.ParseDatabaseBinding inline without
// re-validating shape — parse errors here would be a validator
// regression, not a YAML issue.
func databaseEnvVars(rt *runtime.Runtime, svc config.ServiceSpec) []corev1.EnvVar {
	if len(svc.Databases) == 0 {
		return nil
	}
	out := make([]corev1.EnvVar, 0, len(svc.Databases)*5)
	for _, entry := range svc.Databases {
		prefix, dbName, err := config.ParseDatabaseBinding(entry)
		if err != nil {
			// Validator should have caught this — if a binding slips
			// through, skip silently rather than panic the deploy
			// pipeline. Manifest-render is not the right place for
			// shape errors.
			continue
		}
		secret := naming.DatabaseCredentials(rt.Cfg.App, rt.Cfg.Env, dbName)
		out = append(out,
			secretKeyRefEnv(prefix+"_URL", secret, "url"),
			secretKeyRefEnv(prefix+"_HOST", secret, "host"),
			secretKeyRefEnv(prefix+"_PORT", secret, "port"),
			secretKeyRefEnv(prefix+"_USER", secret, "user"),
			secretKeyRefEnv(prefix+"_PASSWORD", secret, "password"),
		)
	}
	return out
}

// secretKeyRefEnv is the standard SecretKeyRef wrapper. Same shape
// secretEnvVars produces, except the Secret + key are explicit
// (the per-DB credentials Secret, not the shared app-secrets
// Secret).
func secretKeyRefEnv(envName, secret, key string) corev1.EnvVar {
	return corev1.EnvVar{
		Name: envName,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secret},
				Key:                  key,
			},
		},
	}
}
