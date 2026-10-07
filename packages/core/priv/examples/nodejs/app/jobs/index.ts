import { eq } from "drizzle-orm";

import { getDb, jobs as jobsTable } from "~/db";

/**
 * The three jobs this seed ships.
 *
 * ## `pg-boss` runs these, and it owns its own tables
 *
 * On Cloudflare these were plain functions in a map, invoked by the platform's
 * `queue()` handler. Off it there is no platform to invoke anything, so the
 * queue is a library — `pg-boss`, built ON Postgres: `LISTEN/NOTIFY` for
 * wakeups and `SKIP LOCKED` for fetching.
 *
 * The SQLite twin runs `sidequest`, which polls a table. That is the right
 * answer there and the wrong one here, for the same reason the Go pair is goqite
 * against River: a queue that polls a database able to tell it is paying for
 * something it is not using.
 *
 * That is the boundary the other seeds keep too. A queue is not a model —
 * Drizzle describes `jobs` below, and describes nothing of pg-boss's.
 *
 * ## `jobs` is what a PERSON sees; pg-boss's tables are what a WORKER sees
 *
 * They are deliberately two things. `jobs` is somebody's export, its status and
 * its result, kept after it finished; pg-boss's row is work pending and leaves
 * when it is done. Merging them would make "show me my exports" and "give me the
 * next task" the same query, and the second one takes a lock.
 *
 * ## NOTHING HERE ANNOUNCES
 *
 * Each of these called `notifyJobUpdate`, which addressed a `JobRunner` Durable
 * Object by user id and pushed the job into it. There is no addressable object
 * off Cloudflare, and this runs in the WORKER process — it could not reach a
 * socket the server process holds even if there were one.
 *
 * So a job writes the row and stops. The server watches the `jobs` table and
 * fans changes out to whoever is looking (`broadcast.server.ts`). Every status
 * below still reaches the browser live; it arrives because the row changed
 * rather than because this told it to.
 */

// ── AWAITED, BECAUSE THE DRIVER IS ─────────────────────────────────────────
//
// The SQLite twin writes these with `.run()`. `better-sqlite3` reads a file in
// this process, so drizzle's sqlite builder executes synchronously; `pg` talks
// over a socket and cannot, so the builder IS the promise and there is no
// `.run()` to call.

/** What every job here does around its own work. */
async function started(jobId: string) {
  await getDb()
    .update(jobsTable)
    .set({ status: "started", startedAt: new Date().toISOString() })
    .where(eq(jobsTable.id, jobId));
}

async function completed(jobId: string, result: unknown) {
  await getDb()
    .update(jobsTable)
    .set({
      status: "completed",
      result: JSON.stringify(result),
      completedAt: new Date().toISOString(),
    })
    .where(eq(jobsTable.id, jobId));
}

/**
 * THIS DOES NOT SWALLOW. It records the failure on the row a person is looking
 * at, and then RETHROWS so pg-boss sees the job fail — otherwise the queue
 * records a success, and its retry policy describes something that did not
 * happen.
 */
async function failed(jobId: string, error: unknown): Promise<never> {
  await getDb()
    .update(jobsTable)
    .set({
      status: "error",
      error: String(error),
      completedAt: new Date().toISOString(),
    })
    .where(eq(jobsTable.id, jobId));

  throw error;
}

export async function exportJob(jobId: string) {
  try {
    await started(jobId);
    await new Promise((resume) => setTimeout(resume, 3000));
    await completed(jobId, { file: "export.csv", rows: 1000 });
  } catch (error) {
    await failed(jobId, error);
  }
}

export async function emailJob(jobId: string, to?: string) {
  try {
    await started(jobId);
    await new Promise((resume) => setTimeout(resume, 2000));
    await completed(jobId, { sent: true, to });
  } catch (error) {
    await failed(jobId, error);
  }
}

export async function reportJob(jobId: string) {
  try {
    await started(jobId);
    await new Promise((resume) => setTimeout(resume, 4000));
    await completed(jobId, { report: "sales-report.pdf", pages: 25 });
  } catch (error) {
    await failed(jobId, error);
  }
}

/**
 * What the API accepts, and the ONLY thing that does.
 *
 * A client sends `type: "export"`, so something has to turn that string into a
 * class. This map is it — and it being explicit is the point: an unknown type is
 * rejected at the boundary rather than reaching the queue and failing three
 * times in a worker.
 */
export const JOBS = {
  export: exportJob,
  email: emailJob,
  report: reportJob,
} as const;

export type JobType = keyof typeof JOBS;

export function isJobType(type: string): type is JobType {
  return type in JOBS;
}

/** What travels on the queue. One shape, so the worker unpacks what the API packed. */
export type JobPayload = {
  type: string;
  jobId: string;
  to?: string;
};
