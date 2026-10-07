import { vi } from "vitest";

process.env.NVOI_VIEWER_KEY = "test-key";

// Each test file gets a Postgres of its own, in memory (PGlite): the tests need no server and never touch the
// machine's database. The app, its migrations and pg-boss run on it as they do on DATABASE_URL.
vi.mock("~/db/connection", async () => {
  const { resolve } = await import("node:path");
  const { PGlite } = await import("@electric-sql/pglite");
  const { drizzle } = await import("drizzle-orm/pglite");
  const { migrate } = await import("drizzle-orm/pglite/migrator");
  const { fromPglite } = await import("pg-boss");
  const schema = await import("~/db/schema");

  const client = new PGlite();

  return {
    async open() {
      const db = drizzle(client, { schema });
      await migrate(db, { migrationsFolder: resolve(process.cwd(), "migrations") });
      return db;
    },
    queueOptions: () => ({ db: fromPglite(client), backend: "pglite" as const }),
  };
});
