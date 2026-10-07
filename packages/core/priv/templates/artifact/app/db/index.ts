import { open, type Db } from "./connection";

export * from "./schema";
export type { Db };

let opened: Promise<Db> | null = null;

// The database, opened once per process, its migrations run before the first query.
export function getDb(): Promise<Db> {
  opened ??= open().catch((error) => {
    opened = null;
    throw error;
  });

  return opened;
}
