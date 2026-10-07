import { useEffect, useRef, useState } from "react";
import { data, useFetcher } from "react-router";

import type { Route } from "./+types/home";
import { Button, Card, Input, Label } from "~/components";
import type { Note } from "~/db";
import { useLive } from "~/hooks/live";
import { create, list, total } from "~/services/notes.server";
import { enqueue } from "~/services/queue.server";
import { requireViewer, viewer } from "~/services/viewer.server";

export function meta() {
  return [{ title: "Notes" }];
}

export async function loader({ request }: Route.LoaderArgs) {
  const current = viewer(request);

  return { viewer: current, notes: current ? await list(current.id) : [], total: await total() };
}

export async function action({ request }: Route.ActionArgs) {
  const current = requireViewer(request);
  const body = String((await request.formData()).get("body") ?? "").trim();

  if (!body) return data({ error: "Write something first." }, { status: 422 });
  if (body.length > 2000) return data({ error: "Keep it under 2000 characters." }, { status: 422 });

  const note = await create(current.id, body);

  // The word count happens in the jobs process, which broadcasts when done.
  await enqueue("count", { noteId: note.id });

  return { error: null };
}

type Message = { type: "note"; note: Note } | { type: "total"; total: number };

export default function Home({ loaderData }: Route.ComponentProps) {
  const fetcher = useFetcher<typeof action>();
  const form = useRef<HTMLFormElement>(null);
  const [counted, setCounted] = useState<Record<number, Note>>({});
  const [liveTotal, setLiveTotal] = useState<number | null>(null);

  useLive((message) => {
    const event = message as Message;
    if (event.type === "note") setCounted((notes) => ({ ...notes, [event.note.id]: event.note }));
    if (event.type === "total") setLiveTotal(event.total);
  });

  useEffect(() => {
    if (fetcher.state === "idle" && fetcher.data?.error === null) form.current?.reset();
  }, [fetcher.state, fetcher.data]);

  const notes = loaderData.notes.map((note) => counted[note.id] ?? note);
  const sum = Math.max(liveTotal ?? 0, loaderData.total);

  return (
    <main className="mx-auto flex max-w-2xl flex-col gap-6 p-6">
      <header className="flex items-baseline justify-between">
        <h1 className="text-xl font-semibold">Notes</h1>
        <p className="text-sm text-muted-foreground">
          {sum} {sum === 1 ? "note" : "notes"} in total
        </p>
      </header>

      {loaderData.viewer ? (
        <>
          <Card>
            <fetcher.Form method="post" ref={form} className="flex flex-col gap-3">
              <Label htmlFor="body">New note, {loaderData.viewer.name || loaderData.viewer.email}</Label>
              <div className="flex gap-2">
                <Input id="body" name="body" autoComplete="off" />
                <Button type="submit" disabled={fetcher.state !== "idle"}>
                  Add
                </Button>
              </div>
              {fetcher.data?.error && <p className="text-sm text-destructive">{fetcher.data.error}</p>}
            </fetcher.Form>
          </Card>

          <ul className="flex flex-col gap-2">
            {notes.map((note) => (
              <li key={note.id} className="flex justify-between gap-4 rounded-md border px-4 py-3 text-sm">
                <span className="break-words">{note.body}</span>
                <span className="shrink-0 text-muted-foreground">
                  {note.words === null ? "counting" : `${note.words} ${note.words === 1 ? "word" : "words"}`}
                </span>
              </li>
            ))}
          </ul>
        </>
      ) : (
        <p className="text-sm text-muted-foreground">Open this app from nvoi to write notes.</p>
      )}
    </main>
  );
}
