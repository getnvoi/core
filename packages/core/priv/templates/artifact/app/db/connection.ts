import { resolve } from "node:path";

import { drizzle, type NodePgDatabase } from "drizzle-orm/node-postgres";
import { migrate } from "drizzle-orm/node-postgres/migrator";
import pg from "pg";
import type { ConstructorOptions } from "pg-boss";

import * as schema from "./schema";

export type Db = NodePgDatabase<typeof schema>;

// Relative to the project root: every script runs from there, and in the
// production build this module is bundled away from its source folder.
const MIGRATIONS = resolve(process.cwd(), "migrations");

// Any number, the same in every process: the web and jobs processes boot
// together, and only one of them runs the migrations at a time.
const MIGRATION_LOCK = 72_616;

// DATABASE_URL, else the PG* variables (PGHOST, PGUSER, PGPASSWORD, PGDATABASE,
// PGSSLMODE) as libpq reads them. No default: nvoi gives each machine its database.
function connection() {
  if (!process.env.DATABASE_URL && !process.env.PGHOST) throw new Error("DATABASE_URL is not set");

  return { connectionString: process.env.DATABASE_URL || undefined };
}

// The database with its schema brought up to date.
export async function open(): Promise<Db> {
  const pool = new pg.Pool(connection());
  const client = await pool.connect();

  try {
    await client.query("select pg_advisory_lock($1)", [MIGRATION_LOCK]);
    await migrate(drizzle(client), { migrationsFolder: MIGRATIONS });
  } finally {
    await client.query("select pg_advisory_unlock($1)", [MIGRATION_LOCK]).catch(() => {});
    client.release();
  }

  return drizzle(pool, { schema });
}

// pg-boss keeps its tables in the same database, in its own schema `pgboss`.
export function queueOptions(): ConstructorOptions {
  return connection();
}
