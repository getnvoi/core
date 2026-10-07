// The jobs process: takes work off the queue, records the entry, announces it.
//
// Its own process, started and followed separately — the same shape as Rails'
// `bin/jobs`. That is the point being demonstrated: the row a browser sees
// appears because something else ran, not because the request that triggered it
// returned a value.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"nvoi.to/seed/internal/seed"
)

type recorder struct {
	river.WorkerDefaults[seed.RecordEntry]
	pool *pgxpool.Pool
}

func (r *recorder) Work(ctx context.Context, _ *river.Job[seed.RecordEntry]) error {
	var entry seed.Entry

	err := r.pool.QueryRow(ctx,
		"insert into entries default values returning id, created_at",
	).Scan(&entry.ID, &entry.CreatedAt)
	if err != nil {
		return err
	}

	// ANNOUNCED THROUGH POSTGRES, because the web process is a different
	// process. Serialised here rather than in the listener so there is one
	// definition of what a browser receives.
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	_, err = r.pool.Exec(ctx, "select pg_notify($1, $2)", seed.Channel, string(payload))

	return err
}

func main() {
	// STOPS WHEN IT IS ASKED TO. A restart sends a signal, and a worker that
	// ignored it would be killed mid-job with the row half written.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := seed.Pool(ctx)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pool.Close()

	// BOTH BINARIES MIGRATE, and neither may assume the other started first.
	// They are separate services and the manifest's order is a preference, not a
	// barrier — so this is idempotent and runs in whichever comes up.
	if err := seed.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	driver := riverpgxv5.New(pool)

	// THE QUEUE'S OWN TABLES, migrated here rather than in the service's
	// `before:` — only the worker owns them, and River's migrator is idempotent
	// by design, so a replaced box runs it again for free.
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		log.Fatalf("migrator: %v", err)
	}

	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &recorder{pool: pool})

	client, err := river.NewClient(driver, &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 4}},
		Workers: workers,
	})
	if err != nil {
		log.Fatalf("client: %v", err)
	}

	if err := client.Start(ctx); err != nil {
		log.Fatalf("start: %v", err)
	}

	log.Print("worker ready")

	// BLOCKS, because this is a service. `Start` returns as soon as the client
	// is running, so without this the process would exit at once and the
	// follower would report a job worker that crashed on boot.
	<-ctx.Done()

	if err := client.Stop(context.Background()); err != nil {
		log.Printf("stop: %v", err)
	}
}
