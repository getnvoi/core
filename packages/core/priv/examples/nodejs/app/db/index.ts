import { drizzle } from "drizzle-orm/node-postgres";
import { migrate } from "drizzle-orm/node-postgres/migrator";
import { Pool } from "pg";

import * as schema from "./schema";

export * from "./schema";

/**
 * The database, which is somewhere else.
 *
 * ## `DATABASE_URL`, whole, with no assembly
 *
 * nvoi mints the connection and writes it out in every shape a client might
 * look for — `DATABASE_URL`, `POSTGRES_*`, `PG*`, `PGSSLMODE`. This reads the
 * URL, because that is the one spelling `pg` takes without argument.
 *
 * It is NOT assembled from the parts. A URL rebuilt from a host and a password
 * loses `sslmode` and the endpoint parameter that a managed provider routes on,
 * and the failure is a connection timeout that reads as a network problem.
 *
 * ## No default, and no path
 *
 * The SQLite twin creates a file if one is not there. There is nothing to
 * create here: the branch exists before this container did, and a missing
 * variable means provisioning did not finish. Starting anyway gives an app no
 * database and no explanation.
 *
 * ## Migrations run on open, as they do in the twin
 *
 * No separate schema step to forget and no `migrate` command to remember. The
 * first thing that opens the database brings it up to date, so a fresh branch
 * and a restored one reach the same place by the same path.
 */
let handle: ReturnType<typeof drizzle<typeof schema>> | null = null;

export function databaseUrl(): string {
  const url = process.env.DATABASE_URL;

  if (!url) {
    throw new Error(
      "DATABASE_URL is not set. nvoi mints it before this container exists; " +
        "an app started without it has no database and no way to say so.",
    );
  }

  return url;
}

export function getDb() {
  if (handle) return handle;

  // ONE POOL PER PROCESS. `web` and `jobs` are separate processes and each opens
  // its own; a managed provider counts connections, so the size is small on
  // purpose rather than left at the driver's default.
  const pool = new Pool({ connectionString: databaseUrl(), max: 5 });

  handle = drizzle(pool, { schema });

  return handle;
}

/**
 * Bring the schema up to date. Awaited at boot by whichever process starts
 * first — neither may assume the other did it.
 */
export async function migrateDb() {
  await migrate(getDb(), { migrationsFolder: "migrations" });
}
