# Examples

Four starter apps, one per runtime. Each runs a web process, a background worker and Postgres, and shows a
job's progress on the page while it runs, pushed from the worker to the browser.

| Directory | Stack | Jobs | Live updates |
|---|---|---|---|
| `rails/` | Rails 8, Postgres | Solid Queue | Turbo Streams |
| `golang/` | Go, Gin, Postgres | River | server-sent events |
| `rust/` | Rust, actix-web, diesel, Postgres | worker binary | live feed |
| `nodejs/` | React Router 7, drizzle, Postgres | pg-boss | WebSocket |

Each directory's `nvoi.yml` is what a box runs: `setup` once, then `services` in order, each waiting for the
ports of the services before it and running its `before` commands first.

The same files are the catalog: a repository without its own `nvoi.yml` runs under the runtime whose
`detect` files it has at its root (`Nvoi.Manifest`).
