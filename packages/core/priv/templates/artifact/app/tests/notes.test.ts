import { eq } from "drizzle-orm";
import { describe, expect, it } from "vitest";

import { broadcasts, getDb, notes } from "~/db";
import { jobs } from "~/jobs";
import { action, loader } from "~/routes/home";
import { HEADER, sign } from "~/services/viewer.server";

const person = { id: "42", email: "ada@example.com", name: "Ada" };
const header = () => sign(person, Math.floor(Date.now() / 1000) + 60, "test-key");

function post(body: string, signed = true) {
  return new Request("http://localhost/", {
    method: "POST",
    headers: signed ? { [HEADER]: header() } : {},
    body: new URLSearchParams({ body }),
  });
}

const args = (request: Request) => ({ request, params: {}, context: {} }) as never;

describe("notes", () => {
  it("shows an anonymous visitor the total and no notes", async () => {
    const data = await loader(args(new Request("http://localhost/")));
    expect(data.viewer).toBeNull();
    expect(data.notes).toEqual([]);
  });

  it("refuses a note from an anonymous visitor", async () => {
    await expect(action(args(post("hello", false)))).rejects.toMatchObject({ status: 401 });
  });

  it("saves a note, queues its count, and the job counts and broadcasts", async () => {
    await action(args(post("three small words")));

    const data = await loader(args(new Request("http://localhost/", { headers: { [HEADER]: header() } })));
    expect(data.notes).toHaveLength(1);
    expect(data.notes[0].words).toBeNull();

    const db = await getDb();
    const [queued] = (
      await db.execute<{ data: { noteId: number } }>("select data from pgboss.job where name = 'count'")
    ).rows;
    expect(queued.data).toEqual({ noteId: data.notes[0].id });

    await jobs.count({ noteId: data.notes[0].id });

    const [note] = await db.select().from(notes).where(eq(notes.id, data.notes[0].id));
    expect(note?.words).toBe(3);

    const sent = await db.select().from(broadcasts).orderBy(broadcasts.id);
    expect(sent.map((row) => row.channel)).toEqual(["viewer:42", "public"]);
    expect(JSON.parse(sent[1].data)).toEqual({ type: "total", total: 1 });
  });
});
