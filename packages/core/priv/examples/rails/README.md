# Rails 8

Postgres, Solid Queue for jobs and Turbo Streams for live updates.

```
app    3000   Puma
jobs          bin/jobs, Solid Queue
css           tailwindcss:watch
```

`config/database.yml` reads `POSTGRES_HOST`, `POSTGRES_PORT` and `POSTGRES_USER`; `bin/rails db:prepare`
creates and migrates the database before `app` and `jobs` start.
