import compression from "compression";
import express from "express";
import { createServer } from "node:http";
import { WebSocketServer } from "ws";

/**
 * The Node server. On Cloudflare this was `workers/app.ts` — a `fetch` handler,
 * a `queue` handler and a Durable Object export, with `wrangler`/Vite supplying
 * the runtime around it.
 *
 * Here it is one HTTP server carrying two things: React Router, and the
 * WebSocket the browser opens at `/app/jobs/ws`. They SHARE A PORT because a
 * preview is one hostname and one port — `wss://…/app/jobs/ws` has to reach the
 * same place `https://…/app` does, and there is no second port to publish.
 *
 * The jobs worker is NOT here. It is `server/worker.ts`, its own process,
 * started and followed apart — the same shape as Rails' `bin/jobs`. That split
 * is the point being demonstrated: work appears in your browser because
 * something else ran.
 *
 * ## Dev is Vite in middleware mode, and that is the ordinary path
 *
 * This seed runs in DEVELOPMENT: editing a route reloads it, with no build step
 * and no restart. Vite owns the module graph and this owns the socket, which is
 * the only reason there is a custom server at all — `react-router dev` gives the
 * first and has nowhere to put the second.
 */

const PORT = Number(process.env.PORT ?? 3000);
const PRODUCTION = process.env.NODE_ENV === "production";

const app = express();

app.use(compression());

// ── VITE, OR THE BUILD ───────────────────────────────────────────────────────
//
// Imported dynamically because `vite` is a dev dependency: a production install
// does not have it, and a static import would fail at load rather than at the
// branch that needs it.
const vite = PRODUCTION
  ? null
  : await import("vite").then((module) =>
      module.createServer({
        server: { middlewareMode: true },
      }),
    );

if (vite) {
  app.use(vite.middlewares);
} else {
  app.use(
    "/assets",
    express.static("build/client/assets", { immutable: true, maxAge: "1y" }),
  );
  app.use(express.static("build/client", { maxAge: "1h" }));
}

const { createRequestHandler } = await import("@react-router/express");

app.all(
  "*",
  createRequestHandler({
    build: vite
      ? () => vite.ssrLoadModule("virtual:react-router/server-build")
      : // @ts-expect-error — written by `react-router build`, so it does not
        // exist until the build has run. The alternative is a hand-written
        // `.d.ts` describing somebody else's generated file.
        () => import("../build/server/index.js"),
  }),
);

const server = createServer(app);

// ── THE SOCKET ───────────────────────────────────────────────────────────────
//
// `noServer`, with the upgrade routed by path below, because VITE ALSO SPEAKS
// WEBSOCKET on this port — that is how HMR reaches the browser. A `ws` server
// attached to the whole thing would answer Vite's upgrades and break reloading.
const sockets = new WebSocketServer({ noServer: true });

server.on("upgrade", async (request, socket, head) => {
  const path = new URL(request.url ?? "/", "http://localhost").pathname;

  if (path !== "/app/jobs/ws") {
    // NOT OURS — Vite's HMR socket, in dev. Left alone: `noServer` means `ws`
    // has not consumed it, and Vite's own upgrade listener on this server
    // handles it.
    return;
  }

  // AUTHENTICATED BEFORE THE UPGRADE. The Durable Object was addressed by user
  // id from an already-authenticated loader, so the socket inherited that; here
  // the upgrade is a raw HTTP request and the session cookie on it is the only
  // thing that says who is asking. Without this check any client could ask for
  // any user's jobs.
  const { userIdFromRequest } = await load<
    typeof import("../app/services/auth.server")
  >("app/services/auth.server.ts");

  const userId = await userIdFromRequest(request.headers.cookie);

  if (!userId) {
    // A 401 rather than a silent destroy: the browser's `onclose` fires either
    // way, but a person reading devtools sees a reason.
    socket.write("HTTP/1.1 401 Unauthorized\r\n\r\n");
    socket.destroy();

    return;
  }

  const { watch } = await load<
    typeof import("../app/services/broadcast.server")
  >("app/services/broadcast.server.ts");

  sockets.handleUpgrade(request, socket, head, (connection) => {
    watch(userId, connection);
  });
});

/**
 * App code, through whichever module graph is live.
 *
 * In dev that MUST be Vite's: a plain `import` would load a second copy of
 * `broadcast.server.ts` with its own `watchers` map and its own poller, so a
 * socket registered here would never be found by the one Vite's copy is
 * announcing to. In production there is one graph and this is a plain import.
 */
async function load<T>(path: string): Promise<T> {
  if (vite) return vite.ssrLoadModule(`/${path}`) as Promise<T>;

  return import(`../${path.replace(/\.ts$/, ".js")}`) as Promise<T>;
}

// ── ADDRESS THE QUEUE, WITHOUT BECOMING A WORKER ─────────────────────────────
//
// `connectQueue` tells this process where the queue lives so a route can
// enqueue. It is NOT `start`, which would make every web process a worker and
// run each job as many times as there are servers — `server/worker.ts` is the
// one that starts.
//
// Missing entirely, the first `POST /app/api/jobs` failed with
//
//     Error: Engine not configured. Call engine.configure() or engine.start()
//
// which reads as a broken queue rather than a process that never introduced
// itself to one.
const { connectQueue } = await load<
  typeof import("../app/services/queue.server")
>("app/services/queue.server.ts");

await connectQueue();

const { announceChanges } = await load<
  typeof import("../app/services/broadcast.server")
>("app/services/broadcast.server.ts");

announceChanges();

server.listen(PORT, "0.0.0.0", () => {
  console.log(`listening on :${PORT}`);
});
