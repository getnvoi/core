import { gt, lt, max } from "drizzle-orm";
import type { WebSocket } from "ws";

import { broadcasts, getDb } from "~/db";

// Channel for messages every socket receives, signed in or not.
const PUBLIC = "public";

// How often the web process looks for new rows, and how long a row is kept.
const POLL_MS = 250;
const KEEP_MS = 60_000;

// Sends `data` to the sockets of one viewer, or to every socket when `viewerId` is null.
// It writes a row rather than touching sockets, so it works the same from a route
// action in the web process and from a job in the jobs process.
export async function broadcast(viewerId: string | null, data: unknown) {
  await (await getDb())
    .insert(broadcasts)
    .values({ channel: viewerId === null ? PUBLIC : `viewer:${viewerId}`, data: JSON.stringify(data) });
}

// Open sockets by channel. Lives only in the web process.
const channels = new Map<string, Set<WebSocket>>();

// Registers a socket: a signed-in viewer's socket joins its own channel and the
// public one, an anonymous socket only the public one.
export function listen(viewerId: string | null, socket: WebSocket) {
  const joined = viewerId === null ? [PUBLIC] : [PUBLIC, `viewer:${viewerId}`];

  for (const name of joined) {
    let sockets = channels.get(name);
    if (!sockets) channels.set(name, (sockets = new Set()));
    sockets.add(socket);
  }

  socket.on("close", () => {
    for (const name of joined) {
      const sockets = channels.get(name);
      sockets?.delete(socket);
      if (sockets?.size === 0) channels.delete(name);
    }
  });
}

// Started once by the web process: sends new rows to their sockets and deletes old ones.
export async function relay() {
  const db = await getDb();

  // Rows written before this process started are not replayed.
  let last = (await db.select({ id: max(broadcasts.id) }).from(broadcasts))[0]?.id ?? 0;
  let busy = false;

  const tick = async () => {
    if (busy) return;
    busy = true;

    try {
      const rows = await db.select().from(broadcasts).where(gt(broadcasts.id, last)).orderBy(broadcasts.id).limit(500);

      for (const row of rows) {
        last = row.id;

        for (const socket of channels.get(row.channel) ?? []) {
          if (socket.readyState === socket.OPEN) socket.send(row.data);
        }
      }

      await db.delete(broadcasts).where(lt(broadcasts.createdAt, new Date(Date.now() - KEEP_MS)));
    } catch (error) {
      console.error("relay:", error);
    } finally {
      busy = false;
    }
  };

  return setInterval(tick, POLL_MS);
}
