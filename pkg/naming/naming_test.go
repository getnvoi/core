package naming

import "testing"

// TestDatabaseNames locks the single source of truth for every kube-
// side database name. The reconciler + each DatabaseProvider's
// EnsureCredentials + every CLI verb derive names from these helpers —
// drift here breaks SweepOwned (orphans) AND service env wiring (apps
// land on the wrong Secret).
func TestDatabaseNames(t *testing.T) {
	const (
		app    = "myapp"
		env    = "prod"
		dbName = "app"
	)
	prefix := "nvoi-" + app + "-" + env + "-db-" + dbName

	cases := []struct {
		name string
		got  string
		want string
	}{
		{"Database", Database(app, env, dbName), prefix},
		{"DatabasePVC", DatabasePVC(app, env, dbName), prefix + "-data"},
		{"DatabasePod", DatabasePod(app, env, dbName), prefix + "-0"},
		{"DatabaseCredentials", DatabaseCredentials(app, env, dbName), prefix + "-credentials"},
		{"DatabaseBackupCron", DatabaseBackupCron(app, env, dbName), prefix + "-backup"},
		{"DatabaseBackupCreds", DatabaseBackupCreds(app, env, dbName), prefix + "-backup-creds"},
		{"DatabaseBackupBucket", DatabaseBackupBucket(app, env, dbName), "nvoi-" + app + "-" + env + "-db-" + dbName + "-backups"},
		{"DatabaseSnapshot", DatabaseSnapshot(app, env, dbName, "pre-deploy"), prefix + "-snap-pre-deploy"},
		{"DatabaseBranch", DatabaseBranch(app, env, dbName, "pr-142"), prefix + "-br-pr-142"},
		{"DatabaseBranchSnapshot", DatabaseBranchSnapshot(app, env, dbName, "pr-142"), prefix + "-snap-br-pr-142"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
			}
		})
	}
}

// TestPrefixConsistency locks that everything starts with
// `nvoi-{app}-{env}-` — operators rely on this for `kubectl get … |
// grep nvoi-myapp-prod-` listing every nvoi-managed object.
func TestPrefixConsistency(t *testing.T) {
	const (
		app = "myapp"
		env = "prod"
	)
	want := "nvoi-" + app + "-" + env + "-"
	names := []string{
		Database(app, env, "x"),
		DatabasePVC(app, env, "x"),
		DatabaseCredentials(app, env, "x"),
		DatabaseBackupCron(app, env, "x"),
		DatabaseBackupCreds(app, env, "x"),
		DatabaseBackupBucket(app, env, "x"),
		DatabaseSnapshot(app, env, "x", "y"),
		DatabaseBranch(app, env, "x", "y"),
		DatabaseBranchSnapshot(app, env, "x", "y"),
	}
	for _, n := range names {
		if len(n) <= len(want) || n[:len(want)] != want {
			t.Errorf("%q does not start with %q", n, want)
		}
	}
}
