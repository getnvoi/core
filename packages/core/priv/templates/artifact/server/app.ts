import { createServer } from "node:http";

import { createRequestHandler } from "@react-router/express";
import compression from "compression";
import express from "express";
import { WebSocketServer } from "ws";

import { getDb } from "~/db";
import { SOCKET_PATH } from "~/lib/socket";
import { listen, relay } from "~/services/broadcast.server";
import { viewer } from "~/services/viewer.server";

import { edge } from "./edge";

// The web process: React Router and the WebSocket on one port, because the edge
// forwards a single port to the app. Jobs run in server/worker.ts.

const PORT = Number(process.env.PORT ?? 3000);
const PRODUCTION = process.env.NODE_ENV === "production";

// Runs the migrations before the first request.
await getDb();

const app = express();
edge(app);
app.disable("x-powered-by");
app.use(compression());

const server = createServer(app);

// Vite is imported only in development; it serves the app and its HMR socket on this server.
const vite = PRODUCTION
  ? null
  : await import("vite").then(({ createServer }) =>
      createServer({ server: { middlewareMode: true, hmr: { server } } }),
    );

if (vite) {
  app.use(vite.middlewares);
} else {
  app.use("/assets", express.static("build/client/assets", { immutable: true, maxAge: "1y" }));
  app.use(express.static("build/client", { maxAge: "1h" }));
}

app.all(
  "*",
  createRequestHandler({
    build: vite
      ? () => vite.ssrLoadModule("virtual:react-router/server-build")
      : // @ts-expect-error written by `npm run build`
        () => import("../build/server/index.js"),
  }),
);

const sockets = new WebSocketServer({ noServer: true });

// Other upgrade paths are left to Vite's HMR listener in development.
server.on("upgrade", (request, socket, head) => {
  if (new URL(request.url ?? "/", "http://localhost").pathname !== SOCKET_PATH) return;

  // Anonymous sockets are accepted and only join the public channel.
  const current = viewer(request);

  sockets.handleUpgrade(request, socket, head, (connection) => listen(current?.id ?? null, connection));
});

await relay();

server.listen(PORT, "0.0.0.0", () => console.log(`listening on :${PORT}`));

process.on("SIGTERM", () => server.close(() => process.exit(0)));
