package deploy

import (
	"context"
	"fmt"
	"io"

	"github.com/getnvoi/core/pkg/config"
	"github.com/getnvoi/core/pkg/internal/kube"
	"github.com/getnvoi/core/pkg/log"
	"github.com/getnvoi/core/pkg/naming"
	"github.com/getnvoi/core/pkg/providers"
	"github.com/getnvoi/core/pkg/runtime"
	"github.com/getnvoi/core/pkg/ssh"
)

// Database verb layer.
//
// Each function below is a self-contained verb body — opens the
// deploy session, dials the primary's SSH, builds the kube tunnel
// over it, assembles the canonical DatabaseRequest, resolves the
// engine provider, executes the verb's method, returns the typed
// result. cmd/cli is a thin flag-parsing shim that only needs to
// import pkg/deploy + pkg/providers — no pkg/internal/kube reach.
//
// All cleanup is deferred — closing the kube tunnel and the SSH
// connection on return, regardless of the action's error.

// DatabaseSQL executes one SQL statement against the named database.
func DatabaseSQL(ctx context.Context, rt *runtime.Runtime, dbName, stmt string) (*providers.SQLResult, error) {
	var out *providers.SQLResult
	err := withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		res, err := dc.prov.ExecSQL(ctx, dc.req, stmt)
		out = res
		return err
	})
	return out, err
}

// DatabaseBackupNow triggers a one-shot backup Job from the
// scheduled CronJob template.
func DatabaseBackupNow(ctx context.Context, rt *runtime.Runtime, dbName string) (*providers.BackupRef, error) {
	var out *providers.BackupRef
	err := withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		ref, err := dc.prov.BackupNow(ctx, dc.req)
		out = ref
		return err
	})
	return out, err
}

// DatabaseListBackups returns every artifact in the per-DB bucket.
func DatabaseListBackups(ctx context.Context, rt *runtime.Runtime, dbName string) ([]providers.BackupRef, error) {
	var out []providers.BackupRef
	err := withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		refs, err := dc.prov.ListBackups(ctx, dc.req)
		out = refs
		return err
	})
	return out, err
}

// DatabaseDownloadBackup streams one artifact to w.
func DatabaseDownloadBackup(ctx context.Context, rt *runtime.Runtime, dbName, backupID string, w io.Writer) error {
	return withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		return dc.prov.DownloadBackup(ctx, dc.req, backupID, w)
	})
}

// DatabaseRestore replays a backup artifact into the database.
// Destructive — every write since the artifact's timestamp is lost.
// Caller must confirm intent (CLI requires --yes).
func DatabaseRestore(ctx context.Context, rt *runtime.Runtime, dbName, backupID string) error {
	return withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		if dc.req.Bucket == nil {
			return fmt.Errorf("databases.%s: restore requires backup: config (providers.storage + bucket)", dbName)
		}
		return dc.prov.Restore(ctx, dc.req, backupID)
	})
}

// DatabaseRestoreLatest resolves the most-recent backup key (ISO
// timestamps sort lexicographically) and replays it. Convenience
// shortcut on top of DatabaseListBackups + DatabaseRestore.
func DatabaseRestoreLatest(ctx context.Context, rt *runtime.Runtime, dbName string) (string, error) {
	var picked string
	err := withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		if dc.req.Bucket == nil {
			return fmt.Errorf("databases.%s: restore requires backup: config (providers.storage + bucket)", dbName)
		}
		refs, err := dc.prov.ListBackups(ctx, dc.req)
		if err != nil {
			return fmt.Errorf("list backups: %w", err)
		}
		if len(refs) == 0 {
			return fmt.Errorf("no backups available for databases.%s", dbName)
		}
		picked = refs[0].ID
		for _, r := range refs[1:] {
			if r.ID > picked {
				picked = r.ID
			}
		}
		return dc.prov.Restore(ctx, dc.req, picked)
	})
	return picked, err
}

// DatabaseMigrate moves the named DB to the node declared in cfg.
// Composes backup → teardown → apply → restore.
func DatabaseMigrate(ctx context.Context, rt *runtime.Runtime, dbName string) error {
	return withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		return dc.prov.Migrate(ctx, dc.req)
	})
}

// DatabaseSnapshot creates an addressable snapshot of the DB's data.
// label is empty → defaults to a UTC timestamp.
func DatabaseSnapshot(ctx context.Context, rt *runtime.Runtime, dbName, label string) (providers.SnapshotRef, error) {
	var out providers.SnapshotRef
	err := withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		ref, err := dc.prov.Snapshot(ctx, dc.req, label)
		out = ref
		return err
	})
	return out, err
}

// DatabaseListSnapshots returns every snapshot belonging to the DB.
func DatabaseListSnapshots(ctx context.Context, rt *runtime.Runtime, dbName string) ([]providers.SnapshotRef, error) {
	var out []providers.SnapshotRef
	err := withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		refs, err := dc.prov.ListSnapshots(ctx, dc.req)
		out = refs
		return err
	})
	return out, err
}

// DatabaseDeleteSnapshot removes a snapshot by ID.
func DatabaseDeleteSnapshot(ctx context.Context, rt *runtime.Runtime, dbName, snapID string) error {
	return withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		return dc.prov.DeleteSnapshot(ctx, dc.req, snapID)
	})
}

// DatabaseBranch creates an isolated sibling DB rooted at a fresh
// snapshot of the source.
func DatabaseBranch(ctx context.Context, rt *runtime.Runtime, dbName, branchName string) (providers.BranchRef, error) {
	var out providers.BranchRef
	err := withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		ref, err := dc.prov.Branch(ctx, dc.req, branchName)
		out = ref
		return err
	})
	return out, err
}

// DatabaseListBranches returns every branch of dbName.
func DatabaseListBranches(ctx context.Context, rt *runtime.Runtime, dbName string) ([]providers.BranchRef, error) {
	var out []providers.BranchRef
	err := withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		refs, err := dc.prov.ListBranches(ctx, dc.req)
		out = refs
		return err
	})
	return out, err
}

// DatabaseDeleteBranch removes a branch and its underlying snapshot.
func DatabaseDeleteBranch(ctx context.Context, rt *runtime.Runtime, dbName, branchName string) error {
	return withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		return dc.prov.DeleteBranch(ctx, dc.req, branchName)
	})
}

// DatabaseRollback replaces the primary DB's data with a prior
// snapshot. Same Service, same DSN — clients don't reconfigure.
// Destructive.
func DatabaseRollback(ctx context.Context, rt *runtime.Runtime, dbName, snapID string) error {
	return withDB(ctx, rt, dbName, func(ctx context.Context, dc *dbCtx) error {
		return dc.prov.Rollback(ctx, dc.req, snapID)
	})
}

// dbCtx bundles per-verb call-site state. Built once per verb
// invocation inside withDB.
type dbCtx struct {
	req  providers.DatabaseRequest
	prov providers.DatabaseProvider
}

// withDB is the verb boilerplate: open session, dial primary SSH,
// build kube tunnel, assemble req, resolve engine provider, run the
// action, close everything on return.
func withDB(ctx context.Context, rt *runtime.Runtime, dbName string, action func(ctx context.Context, dc *dbCtx) error) error {
	def, ok := rt.Cfg.Databases[dbName]
	if !ok {
		return fmt.Errorf("database %q is not declared in nvoi.yaml", dbName)
	}

	return RunWithSession(ctx, rt, log.KindCluster, func(ctx context.Context, s *Session) error {
		return s.OnPrimary(ctx, func(sh *ssh.Client) error {
			kc, err := kube.New(ctx, sh)
			if err != nil {
				return fmt.Errorf("open kube tunnel: %w", err)
			}
			defer kc.Close()

			req, prov, err := buildVerbRequest(ctx, rt, kc, sh, dbName, def, s.Lg)
			if err != nil {
				return err
			}
			defer prov.Close()
			return action(ctx, &dbCtx{req: req, prov: prov})
		})
	})
}

func buildVerbRequest(ctx context.Context, rt *runtime.Runtime, kc *kube.Client, sh *ssh.Client, name string, def config.DatabaseSpec, lg log.Log) (providers.DatabaseRequest, providers.DatabaseProvider, error) {
	prov, err := providers.ResolveDatabase(def.Engine, nil)
	if err != nil {
		return providers.DatabaseRequest{}, nil, fmt.Errorf("resolve engine %q: %w", def.Engine, err)
	}

	req := providers.DatabaseRequest{
		App:                   rt.Cfg.App,
		Env:                   rt.Cfg.Env,
		Name:                  name,
		FullName:              naming.Database(rt.Cfg.App, rt.Cfg.Env, name),
		Namespace:             naming.Namespace,
		PodName:               naming.DatabasePod(rt.Cfg.App, rt.Cfg.Env, name),
		PVCName:               naming.DatabasePVC(rt.Cfg.App, rt.Cfg.Env, name),
		BackupName:            naming.DatabaseBackupCron(rt.Cfg.App, rt.Cfg.Env, name),
		CredentialsSecretName: naming.DatabaseCredentials(rt.Cfg.App, rt.Cfg.Env, name),
		Spec: providers.DatabaseSpec{
			Engine:  def.Engine,
			Version: def.Version,
			Server:  def.Server,
			Size:    def.Size,
			Region:  def.Region,
		},
		Kube:      kc,
		MasterSSH: sh,
		Log:       lg,
	}
	if def.Credentials != nil {
		req.Spec.User = resolveVar(def.Credentials.User, rt.SecretValues)
		req.Spec.Password = resolveVar(def.Credentials.Password, rt.SecretValues)
		req.Spec.Database = resolveVar(def.Credentials.Database, rt.SecretValues)
	}
	if def.Backup != nil {
		req.Spec.Backup = &providers.DatabaseBackupSpec{
			Schedule:  def.Backup.Schedule,
			Retention: def.Backup.Retention,
		}
		req.BackupCredsSecretName = naming.DatabaseBackupCreds(rt.Cfg.App, rt.Cfg.Env, name)
		if rt.StorageBucket != nil {
			bucketCreds, err := rt.StorageBucket.Credentials(ctx)
			if err != nil {
				return providers.DatabaseRequest{}, nil, fmt.Errorf("bucket credentials: %w", err)
			}
			req.Bucket = &providers.BucketHandle{
				Name:        naming.DatabaseBackupBucket(rt.Cfg.App, rt.Cfg.Env, name),
				Credentials: bucketCreds,
			}
		}
	}
	if ref, err := providers.ResolveDBImage(ctx); err == nil {
		req.DBImageRef = ref
	} else {
		req.DBImageRef = providers.DBImage()
	}
	return req, prov, nil
}
