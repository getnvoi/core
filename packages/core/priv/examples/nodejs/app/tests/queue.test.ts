import { randomUUID } from "node:crypto";

import { describe, expect, it, beforeAll, beforeEach } from "vitest";
import { eq } from "drizzle-orm";

import { databaseUrl, getDb, jobs, users } from "~/db";
import { exportJob } from "~/jobs";
import { QUEUE } from "~/services/queue.server";

import { prepare, truncate } from "./helpers/db";

/**
 * THE ASSUMPTION THE WHOLE ARCHITECTURE RESTS ON, and where each half is proved.
 *
 * A worktree runs this app inside its sandbox with nothing provisioned
 * anywhere — no queue service, no consumer registered. Three of the four demo
 * features hang off that: `export`, `email` and `report` are all queue jobs.
 *
 * ## What is proved HERE
 *
 * That the queue is addressed at this app's OWN database, and that a job's
 * handler records what it did on the row a person is looking at. Both are
 * ordinary function calls against a real file.
 *
 * ## What is proved by `mix nvoi.dogfood progress`, and NOT here
 *
 * That a produced message reaches a worker in the same box. pg-boss runs its
 * consumer in the `jobs` process, and starting one here would mean booting a
 * second connection and a poller inside a unit test to watch a job move — a
 * harness that exists only for tests, which is a second implementation of the
 * thing under test.
 *
 * The dogfood boots the real seed, runs `bin/worker` as its own service and
 * watches a job move. That is where the claim lives.
 *
 * The Cloudflare version proved the same thing with Miniflare and a
 * `wrangler.jsonc` producer/consumer pair. There are no bindings here — the
 * meeting point is a table.
 */
describe("the local queue", () => {
  beforeAll(() => prepare());
  beforeEach(() => truncate());

  it("is addressed at this app's own database, not a service somewhere", () => {
    // ONE CONNECTION. pg-boss keeps its tables in the same database the app
    // uses; if these ever differ the worker drains a queue nothing writes to,
    // and every job sits `queued` forever while both processes report ready.
    expect(databaseUrl()).toBe(process.env.DATABASE_URL);
    expect(QUEUE).toBe("jobs");
  });

  it("records what a job did on the row somebody is watching", async () => {
    const db = getDb();

    const userId = randomUUID();
    db.insert(users).values({ id: userId, email: "queue@example.com" });

    const jobId = randomUUID();
    await db.insert(jobs).values({ id: jobId, userId, type: "export", key: jobId, status: "queued" });

    await exportJob(jobId);

    const [row] = await db.select().from(jobs).where(eq(jobs.id, jobId));

    expect(row.status).toBe("completed");
    expect(JSON.parse(row.result ?? "{}")).toMatchObject({ file: "export.csv", rows: 1000 });
    expect(row.completedAt).toBeTruthy();
  }, 30_000);
});
