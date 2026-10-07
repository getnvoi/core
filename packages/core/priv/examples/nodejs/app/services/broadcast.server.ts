import { gt, sql } from "drizzle-orm";
import type { WebSocket } from "ws";

import { getDb, jobs } from "~/db";

/**
 * Who is watching, and what they are told.
 *
 * ## This replaces a Durable Object
 *
 * On Cloudflare a `JobRunner` DO was addressed by user id, held that user's
 * sockets, and the worker called `notify` on it directly. Off Cloudflare there
 * is no addressable object and no way for the worker PROCESS to reach a socket
 * held by the server PROCESS — they are separate processes, so there is no
 * shared memory to broadcast over.
 *
 * ## So the worker no longer ANNOUNCES; the server WATCHES
 *
 * The worker writes the job row and stops. The server polls for rows that
 * changed since it last looked and fans them out to that user's sockets.
 *
 * The demonstration is unchanged, and it is worth being clear why: the claim was
 * never "the platform can push". It is that work done by a DIFFERENT process
 * shows up in your browser live — which is about two processes sharing a
 * database, not about how one nudges the other.
 *
 * The Go and Rust seeds do exactly this, for exactly this reason.
 */

/** Fast enough to feel immediate, slow enough that an idle app is not a query
 *  per frame. */
const POLL_MS = 400;

/** Every open socket, by user. A user with two tabs has two. */
const watchers = new Map<string, Set<WebSocket>>();

export function watch(userId: string, socket: WebSocket) {
  let sockets = watchers.get(userId);

  if (!sockets) {
    sockets = new Set();
    watchers.set(userId, sockets);
  }

  sockets.add(socket);

  socket.on("close", () => {
    sockets.delete(socket);

    // The LAST tab closing removes the user, so this map is the set of people
    // currently looking rather than everyone who ever did.
    if (sockets.size === 0) watchers.delete(userId);
  });
}

/**
 * The one poller, started once by the server.
 *
 * ONE POLL PER PROCESS, not one per socket: a hundred open tabs cost the same
 * query as one.
 *
 * It tracks the highest `changed_at` it has seen rather than a job id, because a
 * job is INTERESTING SEVERAL TIMES — queued, started, completed — and an id
 * watermark would deliver each job once and never report it finishing.
 */
export function announceChanges() {
  let since = "";

  const changedAt = sql<string>`coalesce(${jobs.completedAt}, ${jobs.startedAt}, ${jobs.createdAt})`;

  // ── ASYNC, BECAUSE THE DRIVER IS ─────────────────────────────────────────
  //
  // The SQLite twin reads a file in this process and its query returns rows.
  // `pg` talks over a socket, so the builder is a promise — and a tick that
  // forgot to await it would iterate a Promise, find nothing, and advance
  // nothing, silently.
  //
  // Ticks do not overlap: `setInterval` fires this again on schedule, and a slow
  // read would let two run at once. `running` is the guard — a skipped tick
  // costs 400ms of latency, two concurrent ones deliver every row twice.
  let running = false;

  const tick = async () => {
    if (running) return;
    running = true;

    const db = getDb();

    let changed;

    try {
      changed = await db
        .select()
        .from(jobs)
        .where(since ? gt(changedAt, since) : sql`1 = 1`)
        .orderBy(changedAt);
    } catch (error) {
      // A READ THAT FAILED IS NOT A JOB THAT DID NOT CHANGE. The next tick asks
      // again; advancing `since` here would skip it forever.
      console.error("announce:", error);

      running = false;

      return;
    }

    for (const job of changed) {
      since =
        job.completedAt ?? job.startedAt ?? job.createdAt;

      for (const socket of watchers.get(job.userId) ?? []) {
        socket.send(JSON.stringify({ key: job.key, data: job }));
      }
    }

    running = false;
  };

  // FIRST TICK SETS THE WATERMARK and sends nothing to nobody — there are no
  // sockets yet at boot, and every job in the table would otherwise be
  // "changed".
  void tick();

  return setInterval(() => void tick(), POLL_MS);
}
