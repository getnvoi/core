import { sql } from "drizzle-orm";

import { getDb, migrateDb } from "~/db";

/**
 * The REAL database, opened on the path `setup.ts` set.
 *
 * ## There is nothing to stand in for
 *
 * The Cloudflare version of this seed booted Miniflare to get a D1 binding, and
 * the helper it replaces did that here too — an emulator, an in-process worker,
 * and a `dispose()` every suite had to remember. None of it applies: off
 * Cloudflare the database is a FILE this process opens, so a test opens it and
 * the code under test is the code that ships.
 *
 * The migrations run on the first open (see `app/db`), so a fresh temporary file
 * arrives with the schema already on it and there is no `initTestDb` step.
 */
export function testDb() {
  return getDb();
}

/** The schema, applied once — `app/db` does not migrate on open here. */
export async function prepare() {
  await migrateDb();
}

/**
 * Empty every table, keeping the schema.
 *
 * Ordered so children go before parents — foreign keys are ON, and a delete that
 * violates one fails rather than cascading silently. Listed explicitly rather
 * than discovered, because a table added without a thought about ordering should
 * break here and not somewhere later.
 */
export async function truncate() {
  const db = getDb();

  for (const table of [
    "jobs",
    "workspace_invites",
    "workspace_users",
    "workspaces",
    "system_users",
    "users",
  ]) {
    await db.execute(sql.raw(`DELETE FROM "${table}"`));
  }
}
