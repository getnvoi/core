import { PgBoss } from "pg-boss";

import { getDb } from "~/db";
import { queueOptions } from "~/db/connection";
import { type JobData, type JobName, jobs } from "~/jobs";

let started: Promise<PgBoss> | null = null;

// pg-boss on the app's database, one queue per job. Only the jobs process
// supervises and works the queues, so each job runs once; the web process sends.
function queue(worker = false): Promise<PgBoss> {
  started ??= (async () => {
    // The app's migrations first, so a fresh database is set up in one order.
    await getDb();

    const boss = new PgBoss({ ...queueOptions(), supervise: worker, schedule: worker });
    boss.on("error", (error) => console.error("queue:", error));
    await boss.start();

    for (const name of Object.keys(jobs)) await boss.createQueue(name);

    return boss;
  })();

  return started;
}

export async function enqueue<N extends JobName>(name: N, data: JobData<N>) {
  await (await queue()).send(name, data);
}

// Called by the jobs process only.
export async function work() {
  const boss = await queue(true);

  for (const name of Object.keys(jobs) as JobName[]) {
    await boss.work<JobData<typeof name>>(name, async (batch) => {
      for (const job of batch) await jobs[name](job.data);
    });
  }

  return boss;
}
