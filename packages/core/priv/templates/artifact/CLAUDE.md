# App

This folder is a React Router project that nvoi runs in one of two ways:

- **An app** (a repository of its own, made from the Apps page): a server with its Postgres database, a job
  worker (pg-boss) and WebSocket broadcasts. `nvoi.yml` says how it runs: `npm ci`, then the `jobs` and
  `app` services. Each checkout has its own branch of the database.
- **An artifact of kind app**: built static (`npm run build:static`), a single-page app with no server and no
  database. Only `app/routes/page.tsx` (and the routes you add without a loader or an action) is built.

It starts as a small notes example in both modes; replace the example with what you are asked to build.

## A static page

When what is asked is a page to look at (a mockup, a report, a document), build only that: the markup in
the route, styles in `app/app.css`, data written into the route. Leave the database, the jobs and the live
updates as they are and unused; do not delete them.

## Scripts

- `npm run dev`: development server with Vite and reload, on `$PORT` (default 3000).
- `npm run jobs`: the job worker. Restart it after editing a job.
- `npm run build`: production build into `build/`, with its server.
- `npm run build:static`: the single-page build into `build/client` (`NVOI_STATIC=1`), no server.
- `npm run start`: production server on `$PORT`, serving `build/` and the WebSocket on the same port.
- `npm run start:static`: the static build served on `$PORT`, every path falling back to `index.html`. To
  preview the static build as the app, the `app` service in `nvoi.yml` runs
  `npm run build:static && exec npm run start:static` and the `jobs` service is removed.
- `npm run typecheck`, `npm test`.

## Environment

- `PORT`: the one port the preview forwards. HTTP and the WebSocket share it.
- `DATABASE_URL`, and `PGHOST`, `PGUSER`, `PGPASSWORD`, `PGDATABASE`, `PGSSLMODE`: the checkout's Postgres
  branch, set by nvoi. Never point the app at another database.
- `NVOI_VIEWER_KEY`: the key that signs the viewer header.
- `SESSION_SECRET`: available if you need to sign something of your own.

Without `NVOI_VIEWER_KEY` and outside production, every request is the fixed dev viewer `{ id: "dev" }`.

## Where things go

- Routes: `app/routes/`, registered in `app/routes.ts`. Use loaders and actions for data (app mode only).
- Schema: `app/db/schema.ts` (Drizzle, `drizzle-orm/pg-core`). After changing it, run
  `npx drizzle-kit generate` and keep the new file in `migrations/`; both processes run the migrations on
  boot. Query with `const db = await getDb()` from `~/db`.
- Server code: `app/services/*.server.ts`.
- Jobs: functions in `jobs` in `app/jobs/index.ts`, by the name they are queued under. Queue one with
  `await enqueue("count", { noteId })` from `~/services/queue.server` (see `app/routes/home.tsx`). pg-boss
  keeps its tables in the same database, in the schema `pgboss`.
- Components: `app/components/` (Button, Card, Input, Label) and Tailwind 4 in `app/app.css`.

## The viewer

nvoi's edge signs the person looking into the `x-nvoi-viewer` header. Read it on the server:

```ts
import { requireViewer, viewer } from "~/services/viewer.server";

const current = viewer(request); // { id, email, name } or null for an anonymous visitor
const signedIn = requireViewer(request); // throws a 401 Response when anonymous
```

Key rows by `viewer.id`. There is no sign-up, login or users table, and there are no cookies: the app is
framed cross-site, where cookies are unreliable. If you ever need one, set it with
`SameSite=None; Secure; Partitioned`.

## Live updates

```ts
import { broadcast } from "~/services/broadcast.server";

await broadcast(viewer.id, { type: "note", note }); // that viewer's open tabs
await broadcast(null, { type: "total", total }); // every open tab, signed in or not
```

`broadcast` works from routes and from jobs: it writes a row, and the web process sends it to the
sockets at `/socket`. In a component, `useLive((message) => ...)` from `~/hooks/live` receives them.
Anonymous sockets receive only `broadcast(null, ...)`.

## Rules

- No new services: one web process (`start` or `dev`), one worker (`jobs`). No Redis or other servers.
- Postgres only, through `DATABASE_URL`. Do not write data files.
- Keep the scripts in `package.json` as they are; nvoi calls them by name.
- Run `npm run build` (and `npm run typecheck`, `npm test`) before committing. The tests run on an in-memory
  Postgres (PGlite, `app/tests/setup.ts`), never on the checkout's database.
- The app is reached only through nvoi's edge. `server/edge.ts` makes forms work behind it: leave it
  and its place in `server/app.ts` as they are. If a form posted from the page fails with 400 "Bad
  Request", run `npm run build && npm test`: `app/tests/edge.test.ts` posts through Express exactly as
  the edge does. Test a route's action through the server too, not only by calling it.
