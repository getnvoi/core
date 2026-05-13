// cmd/db is the entrypoint for the `docker.io/nvoi/db` image — the
// uniform database backup runner every DatabaseProvider's CronJob
// invokes. Restore (MODE=restore) is reserved for the follow-up PR
// that ships the destructive verbs; today the image is backup-only.
//
// Contract:
//
//	ENV (injected by the CronJob — see providers.BuildBackupCronJob):
//	  MODE                backup (default)
//	  ENGINE              postgres
//	  DATABASE_NAME       logical name (the YAML key, e.g. "app")
//	  DATABASE_FULL_NAME  nvoi-{app}-{env}-db-{name}
//	  DB_HOST             hostname / Service name
//	  DB_PORT             port (5432 / 3306 / vendor-specific)
//	  DB_USER             SQL user
//	  DB_PASSWORD         SQL password
//	  DB_DATABASE         logical SQL database name
//	  DB_SSLMODE          (optional) postgres-style sslmode value;
//	                      passed to PGSSLMODE for pg_dump.
//	  BUCKET_ENDPOINT     S3-compatible base URL
//	  BUCKET_NAME         target bucket (one-per-database)
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
// we read each directly and pass them straight to pg_dump.
//
// Pipeline:
//
//	MODE=backup (default):
//	  1. Pick dump tool (pg_dump for postgres).
//	  2. Stream dump → gzip → temp file at /tmp/backup.sql.gz.
//	  3. Stat the file for content-length.
//	  4. PUT to s3://$BUCKET_NAME/<YYYYMMDDTHHMMSSZ>.sql.gz via sigv4.
//	  5. Delete the temp file; exit 0 on success.
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
// struct so dumpCommand takes one arg, not five.
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
	default:
		// MODE=restore is reserved for the follow-up PR that ships
		// the destructive verbs. Until then, the image is backup-only
		// — restoring is operator-driven via `nvoi database backup
		// download` + a manual psql/pg_restore.
		return fmt.Errorf("unknown MODE %q (expected: backup)", mode)
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
// /proc/<pid>/cmdline.
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
