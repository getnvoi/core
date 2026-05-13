// cmd/db is the entrypoint for the `docker.io/nvoi/db` image — the
// uniform database backup AND restore runner every DatabaseProvider's
// CronJob / Job invokes.
//
// Contract:
//
//	ENV (injected by the CronJob — see providers.BuildBackupCronJob —
//	     or the Job — see providers.BuildRestoreJob):
//	  MODE                backup (default) | restore
//	  ENGINE              postgres
//	  DATABASE_NAME       logical name (the YAML key, e.g. "app")
//	  DATABASE_FULL_NAME  nvoi-{app}-{env}-db-{name}
//	  DB_HOST             hostname / Service name
//	  DB_PORT             port (5432 / 3306 / vendor-specific)
//	  DB_USER             SQL user
//	  DB_PASSWORD         SQL password
//	  DB_DATABASE         logical SQL database name
//	  DB_SSLMODE          (optional) postgres-style sslmode value;
//	                      passed to PGSSLMODE for pg_dump/psql.
//	  BUCKET_ENDPOINT     S3-compatible base URL
//	  BUCKET_NAME         target bucket (one-per-database)
//	  BACKUP_KEY          (restore mode only) S3 object key to replay
//	  AWS_ACCESS_KEY_ID   sigv4 signing key
//	  AWS_SECRET_ACCESS_KEY
//	  AWS_REGION          S3 region ("auto" for R2)
//
// Each DB_* variable is bound to a single Secret key in the CronJob /
// Job spec via SecretKeyRef (see providers.dbCredsEnv). NOT envFrom —
// envFrom doesn't uppercase Secret keys, and the credentials Secret
// uses lowercase keys for Go-side reads.
//
// No DSN handling here. The Secret carries every field separately, so
// we read each directly and pass them straight to pg_dump / psql.
//
// Pipelines:
//
//	MODE=backup (default):
//	  1. Pick dump tool (pg_dump for postgres).
//	  2. Stream dump → gzip → temp file at /tmp/backup.sql.gz.
//	  3. Stat the file for content-length.
//	  4. PUT to s3://$BUCKET_NAME/<YYYYMMDDTHHMMSSZ>.sql.gz via sigv4.
//	  5. Delete the temp file; exit 0 on success.
//
//	MODE=restore:
//	  1. s3.GetStream(BUCKET_NAME, BACKUP_KEY) → io.ReadCloser.
//	  2. Pipe through gzip.NewReader (decompression).
//	  3. Pipe into engine's restore tool (psql) connected to the same
//	     host the dump came from (or whatever DB_HOST points at).
//	  4. Exit 0 on success, non-zero + stderr on failure.
//
// Uniformity is load-bearing — same image handles both directions,
// same Secret feeds both, list/download are bucket-level. Every
// DatabaseProvider routes through here.
package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/getnvoi/core/pkg/utils/s3"
)

const (
	tmpPath       = "/tmp/backup.sql.gz"
	uploadTimeout = 30 * time.Minute
)

// dbCreds is the runtime view of the credentials Secret. Populated
// from DB_HOST / DB_PORT / DB_USER / DB_PASSWORD / DB_DATABASE /
// DB_SSLMODE — every key bound by providers.dbCredsEnv. Single
// struct so dumpCommand / restoreCommand take one arg, not five.
type dbCreds struct {
	host     string
	port     string
	user     string
	password string
	database string
	sslmode  string // optional — empty for engines that don't expose it
}

func loadDBCreds() dbCreds {
	return dbCreds{
		host:     mustEnv("DB_HOST"),
		port:     mustEnv("DB_PORT"),
		user:     mustEnv("DB_USER"),
		password: mustEnv("DB_PASSWORD"),
		database: mustEnv("DB_DATABASE"),
		sslmode:  os.Getenv("DB_SSLMODE"),
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "nvoi-db: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	mode := os.Getenv("MODE")
	if mode == "" {
		mode = "backup"
	}
	switch mode {
	case "backup":
		return runBackup()
	case "restore":
		return runRestore()
	default:
		return fmt.Errorf("unknown MODE %q (expected: backup | restore)", mode)
	}
}

func runBackup() error {
	engine := mustEnv("ENGINE")
	creds := loadDBCreds()
	bucketEndpoint := mustEnv("BUCKET_ENDPOINT")
	bucketName := mustEnv("BUCKET_NAME")
	accessKey := mustEnv("AWS_ACCESS_KEY_ID")
	secretKey := mustEnv("AWS_SECRET_ACCESS_KEY")
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "auto"
	}
	_ = region // signed inside s3.PutStream via env-equivalent context

	// 1. Pick dump tool.
	dumpCmd, err := dumpCommand(engine, creds)
	if err != nil {
		return err
	}

	// 2. Stream dump → gzip → temp file. `pg_dump | gzip` keeps
	// memory flat; failures on either side propagate via the
	// command's exit code or gzip.Writer.Close().
	if err := runDumpToGzippedFile(dumpCmd, tmpPath); err != nil {
		return fmt.Errorf("dump: %w", err)
	}
	defer os.Remove(tmpPath)

	// 3. Stat for content-length.
	fi, err := os.Stat(tmpPath)
	if err != nil {
		return fmt.Errorf("stat dump: %w", err)
	}
	size := fi.Size()
	if size == 0 {
		return fmt.Errorf("dump is empty — refusing to upload a zero-byte backup (dump tool probably failed silently)")
	}

	// 4. Upload.
	key := time.Now().UTC().Format("20060102T150405Z") + ".sql.gz"
	f, err := os.Open(tmpPath)
	if err != nil {
		return fmt.Errorf("open dump: %w", err)
	}
	defer f.Close()

	if err := s3.PutStream(
		strings.TrimRight(bucketEndpoint, "/"),
		accessKey, secretKey, bucketName, key,
		f, size, uploadTimeout,
	); err != nil {
		return fmt.Errorf("upload %s/%s: %w", bucketName, key, err)
	}

	fmt.Printf("uploaded %s/%s (%d bytes, engine=%s)\n", bucketName, key, size, engine)
	return nil
}

// runRestore pulls a backup object from the bucket, gunzips, and
// pipes into the engine's native restore tool against the database.
// Same image, same Secret as backup — just the direction flips.
// Works identically for selfhosted (in-cluster Service) and SaaS
// (external TLS) because host/port/user/password are read from env,
// not inferred from a DSN.
//
// Exit discipline: the restore tool's exit code is the Job's exit
// code. ON_ERROR_STOP=1 (psql) means the first SQL error stops the
// replay, so a partial restore fails loudly rather than leaving a
// half-populated database.
func runRestore() error {
	engine := mustEnv("ENGINE")
	creds := loadDBCreds()
	bucketEndpoint := mustEnv("BUCKET_ENDPOINT")
	bucketName := mustEnv("BUCKET_NAME")
	backupKey := mustEnv("BACKUP_KEY")
	accessKey := mustEnv("AWS_ACCESS_KEY_ID")
	secretKey := mustEnv("AWS_SECRET_ACCESS_KEY")

	rc, _, _, err := s3.GetStream(
		strings.TrimRight(bucketEndpoint, "/"),
		accessKey, secretKey, bucketName, backupKey,
	)
	if err != nil {
		return fmt.Errorf("download %s/%s: %w", bucketName, backupKey, err)
	}
	defer rc.Close()

	gzr, err := gzip.NewReader(rc)
	if err != nil {
		return fmt.Errorf("gunzip %s/%s: %w", bucketName, backupKey, err)
	}
	defer gzr.Close()

	restoreCmd, err := restoreCommand(engine, creds)
	if err != nil {
		return err
	}
	restoreCmd.Stdin = gzr
	restoreCmd.Stdout = os.Stdout
	restoreCmd.Stderr = os.Stderr
	if err := restoreCmd.Run(); err != nil {
		return fmt.Errorf("restore tool exited: %w", err)
	}
	fmt.Printf("restored %s/%s (engine=%s)\n", bucketName, backupKey, engine)
	return nil
}

// restoreCommand returns the exec.Cmd that reads SQL from stdin and
// applies it to the database described by creds. Mirrors dumpCommand
// in structure — the image owns tool selection, nvoi core does not.
//
// postgres → `psql -h/-p/-U/-d` with PGPASSWORD set on env;
//
//	ON_ERROR_STOP=1 aborts the replay on the first SQL error
//	rather than leaving a half-populated DB.
//
// Additional engines land here when they ship — same shape
// (build *exec.Cmd that reads SQL on stdin).
func restoreCommand(engine string, creds dbCreds) (*exec.Cmd, error) {
	switch engine {
	case "postgres":
		cmd := exec.Command("psql",
			"-v", "ON_ERROR_STOP=1",
			"-h", creds.host,
			"-p", creds.port,
			"-U", creds.user,
			"-d", creds.database,
		)
		cmd.Env = pgEnv(creds)
		return cmd, nil
	default:
		return nil, fmt.Errorf("unknown ENGINE %q (expected: postgres)", engine)
	}
}

// dumpCommand returns the exec.Cmd that produces a SQL dump on
// stdout for the requested engine. Kept here (not in pkg/) because
// this is the image's contract — the image owns dump-tool
// selection; nvoi core does not.
//
// postgres → `pg_dump -h/-p/-U/-d` with PGPASSWORD set on env.
//
//	--no-owner / --no-acl strip role-specific metadata so the
//	dump replays cleanly into any target user.
//	--clean --if-exists emit `DROP … IF EXISTS` before each
//	`CREATE`, so a restore onto a populated database
//	replaces existing objects instead of erroring with
//	"relation already exists". `restore` semantics across
//	the surface are "replay this snapshot", which only works
//	if the dump is idempotent.
//
// Additional engines land here when they ship — same shape
// (build *exec.Cmd that writes a SQL dump on stdout).
func dumpCommand(engine string, creds dbCreds) (*exec.Cmd, error) {
	switch engine {
	case "postgres":
		cmd := exec.Command("pg_dump",
			"--no-owner", "--no-acl",
			"--clean", "--if-exists",
			"-h", creds.host,
			"-p", creds.port,
			"-U", creds.user,
			"-d", creds.database,
		)
		cmd.Env = pgEnv(creds)
		return cmd, nil
	default:
		return nil, fmt.Errorf("unknown ENGINE %q (expected: postgres)", engine)
	}
}

// pgEnv layers PGPASSWORD (and PGSSLMODE when DB_SSLMODE is set) on
// top of the parent process env, which is the canonical way to feed
// libpq tooling — passing the password on argv would leak it via
// /proc/<pid>/cmdline. Used by both pg_dump (backup) and psql
// (restore) so the contract is identical.
func pgEnv(creds dbCreds) []string {
	env := append(os.Environ(), "PGPASSWORD="+creds.password)
	if creds.sslmode != "" {
		env = append(env, "PGSSLMODE="+creds.sslmode)
	}
	return env
}

// runDumpToGzippedFile runs the dump command, piping its stdout
// through gzip into the destination file. Stderr is forwarded so a
// failing dump surfaces its message in the k8s Job's pod logs.
func runDumpToGzippedFile(cmd *exec.Cmd, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	gz := gzip.NewWriter(out)
	defer gz.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := io.Copy(gz, stdout); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("dump tool exited: %w", err)
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return out.Close()
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "nvoi-db: missing required env var %s\n", key)
		os.Exit(1)
	}
	return v
}
