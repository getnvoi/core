import { eq } from "drizzle-orm";

import { getDb, notes } from "~/db";
import { broadcast } from "~/services/broadcast.server";
import { total } from "~/services/notes.server";

// Every job, by the name it is queued under, and what it does with its data.
// The jobs process works each of them (server/worker.ts); queue one with
// `enqueue("count", { noteId })` from ~/services/queue.server.
export const jobs = {
  // Counts the words of a note, then sends the note to its owner and the new
  // total to everyone.
  async count({ noteId }: { noteId: number }) {
    const db = await getDb();

    const [note] = await db.select().from(notes).where(eq(notes.id, noteId));
    if (!note) return;

    const words = note.body.trim().split(/\s+/).filter(Boolean).length;
    const [updated] = await db.update(notes).set({ words }).where(eq(notes.id, noteId)).returning();

    await broadcast(note.viewerId, { type: "note", note: updated });
    await broadcast(null, { type: "total", total: await total() });
  },
};

export type JobName = keyof typeof jobs;
export type JobData<N extends JobName> = Parameters<(typeof jobs)[N]>[0];
