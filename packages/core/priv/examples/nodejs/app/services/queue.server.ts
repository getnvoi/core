import PgBoss from "pg-boss";

import { databaseUrl } from "~/db";

/**
 * `pg-boss`, in the one place both processes read it.
 *
 * ## The same database, its own tables
 *
 * It persists to the Postgres branch this app already has, in a schema of its
 * own. Drizzle describes `app/db/schema.ts` and nothing of pg-boss's — the same
 * boundary goqite, apalis and River keep in the other seeds.
 *
 * ## Why not the SQLite twin's queue
 *
 * `sidequest` ships a Postgres backend, so keeping it would have been a
 * one-line swap. It is the wrong one: pg-boss is built ON Postgres —
 * `LISTEN/NOTIFY` for wakeups, `SKIP LOCKED` for fetching — and a queue that
 * polls a table it could be told about is a queue paying for a database it is
 * not using. The twins differ in the same way the Go and Rust pairs do.
 *
 * ## Both processes connect; only one WORKS
 *
 * The server sends, so it needs a started instance and nothing else. The worker
 * calls `work`, which is what makes it a consumer. If the server did too, every
 * web process would run jobs and a job would run as many times as there are
 * servers.
 */

// THE QUEUE'S NAME, in one place because two spellings is a job sent to a queue
// nobody reads.
export const QUEUE = "jobs";

let boss: PgBoss | null = null;

export async function connectQueue(): Promise<PgBoss> {
  if (boss) return boss;

  boss = new PgBoss({
    connectionString: databaseUrl(),

    // SMALL ON PURPOSE. A managed provider counts connections and this process
    // already holds an application pool; the default of 10 doubles the count
    // for a queue that is mostly idle.
    max: 3,
  });

  // NOT SWALLOWED. pg-boss emits errors rather than throwing them from `send`,
  // so with no listener a failed job write is a silent no-op — the row says
  // `queued` and nothing ever moves it.
  boss.on("error", (error) => console.error("[queue]", error));

  await boss.start();

  // ── CREATED, BECAUSE v10 DOES NOT CREATE IT FOR YOU ──────────────────────
  //
  // `send` to a queue that does not exist resolves with a null id and drops the
  // job. Idempotent, so both processes may call it and neither has to assume
  // the other started first.
  await boss.createQueue(QUEUE);

  return boss;
}
