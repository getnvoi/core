// What both binaries need: the row, the job, and how the worker tells the web
// process that one happened.
//
// # The two processes talk through Postgres, not through each other
//
// `web` enqueues and `worker` runs the job — separate processes, so there is no
// shared memory to broadcast over. The worker announces a finished job with
// NOTIFY and the web process is LISTENing; Postgres is already the queue and
// already required, so this adds no service and no second story about delivery.
//
// An in-process channel would be simpler and would only work while both halves
// were one program — which is exactly what the services pattern splits apart.
//
// # Migrations are the APP's job, not a step beside it
//
// There is no `db/schema.sql` and no schema step in the manifest. `Migrate` runs
// at boot, from this file, so the schema travels with the code that expects it
// and cannot drift from it — the same rule the SQLite twin keeps with gorm's
// `AutoMigrate`.
//
// Both binaries call it, and it is idempotent, because NEITHER MAY ASSUME THE
// OTHER STARTED FIRST: they are separate services and the manifest's order is a
// preference, not a barrier.
package seed

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

// The channel both halves agree on.
const Channel = "entries"

// One row, and the shape sent to a browser.
type Entry struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// RecordEntry is the job. It carries nothing: what it does is "record an
// entry", and a payload would be a fact the web process guessed on the
// worker's behalf.
type RecordEntry struct{}

// Kind is River's identifier for this job type. A string rather than the Go
// type name, because renaming a struct must not orphan the rows already queued
// under the old name.
func (RecordEntry) Kind() string { return "record_entry" }

// DatabaseURL is DATABASE_URL, whole, with no assembly and no default.
//
// It used to be built from PG* with `127.0.0.1` and `seed_development` as
// fallbacks. That was written when Postgres ran inside this container; the
// database is a branch outside it now, and those defaults meant the app dialled
// a cluster that is not there — connecting to nothing while reporting a bad
// password, which is the failure a fallback always produces.
//
// Missing is FATAL. An app with no database cannot serve, and a process that
// exits saying so is worth more than one that binds a port and 500s every
// request.
func DatabaseURL() string {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		return url
	}

	panic("DATABASE_URL is not set: this app cannot run without a database")
}

// Pool opens the connection pool both binaries use.
func Pool(ctx context.Context) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, DatabaseURL())
}

// Migrate brings the database up to date: this app's table, then River's own.
//
// TWO OWNERS, IN ORDER. `entries` is ours; River's six tables and its lock
// protocol are its, and describing them here would be half-describing somebody
// else's schema. `rivermigrate` applies what the version of River compiled into
// this binary expects, which is the only thing that can be right.
//
// Idempotent on both halves: `if not exists` here, and River's migrator is a
// no-op at the current version.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	const entries = `
		create table if not exists entries (
			id bigserial primary key,
			created_at timestamptz not null default now()
		)`

	if _, err := pool.Exec(ctx, entries); err != nil {
		return fmt.Errorf("creating entries: %w", err)
	}

	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("river migrator: %w", err)
	}

	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("river migrate: %w", err)
	}

	return nil
}
