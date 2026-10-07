import { count, desc, eq } from "drizzle-orm";

import { getDb, notes } from "~/db";

export async function list(viewerId: string) {
  return (await getDb()).select().from(notes).where(eq(notes.viewerId, viewerId)).orderBy(desc(notes.id));
}

export async function total(): Promise<number> {
  const [row] = await (await getDb()).select({ value: count() }).from(notes);
  return row?.value ?? 0;
}

export async function create(viewerId: string, body: string) {
  const [note] = await (await getDb()).insert(notes).values({ viewerId, body }).returning();
  return note;
}
