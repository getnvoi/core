# React Router 7 on Node

Server-rendered React, Postgres through drizzle, pg-boss jobs and a WebSocket on the app's port.

```
app    3000   React Router (SSR) and the /app/jobs/ws socket
jobs          pg-boss, running background jobs
```

`app` enqueues and `jobs` runs the work; the page follows each job from `queued` to `completed` without a
reload. Both processes read `DATABASE_URL` and run the migrations in `migrations/` at boot.
