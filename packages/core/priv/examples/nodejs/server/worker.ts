import { migrateDb } from "~/db";
import { JOBS, isJobType, type JobPayload } from "~/jobs";
import { connectQueue, QUEUE } from "~/services/queue.server";

/**
 * The jobs process: pg-boss takes work off the queue and runs it.
 *
 * Its OWN process, started and followed separately — the same shape as Rails'
 * `bin/jobs`, Go's `cmd/worker` and Rust's `bin/worker`.
 *
 * `work` is what makes this a consumer, and it is called ONLY here. The server
 * connects to send; if it called this too, every web process would run jobs and
 * a job would run as many times as there are servers.
 *
 * It announces nothing when it is done. The server watches the `jobs` table and
 * fans changes out to whoever is looking — see `broadcast.server.ts`.
 */

// NEITHER PROCESS MAY ASSUME THE OTHER STARTED FIRST. Both migrate; it is
// idempotent, and a worker that ran against a schema the server had not created
// yet would fail on its first job rather than at boot.
await migrateDb();

const boss = await connectQueue();

await boss.work<JobPayload>(QUEUE, async ([job]) => {
  const { type, jobId, to } = job.data;

  // REJECTED HERE TOO, not only at the API boundary. A queue outlives a deploy,
  // so a job enqueued by an older version can name something this one does not
  // have — and running `undefined` is a crash loop with a retry policy.
  if (!isJobType(type)) {
    throw new Error(`unknown job type: ${type}`);
  }

  await JOBS[type](jobId, to);
});

console.log("worker ready");

// SIGTERM IS HOW THIS ENDS. The agent stops a service by signalling it, and
// `stop` lets a job in flight finish rather than leaving it to time out.
process.on("SIGTERM", () => {
  void boss.stop();
});
