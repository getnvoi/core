import { useState } from "react";

import { Button, Card, Input, Label } from "~/components";

// The static build's page: everything runs in the browser and nothing is stored on a server. Data that must
// last lives in the page itself or in the browser (localStorage).
export function meta() {
  return [{ title: "Notes" }];
}

export default function Page() {
  const [notes, setNotes] = useState<string[]>([]);
  const [body, setBody] = useState("");

  return (
    <main className="mx-auto flex max-w-2xl flex-col gap-6 p-6">
      <h1 className="text-xl font-semibold">Notes</h1>
      <Card>
        <form
          className="flex flex-col gap-3"
          onSubmit={(event) => {
            event.preventDefault();
            if (body.trim()) setNotes((all) => [body.trim(), ...all]);
            setBody("");
          }}
        >
          <Label htmlFor="body">New note</Label>
          <div className="flex gap-2">
            <Input id="body" value={body} onChange={(event) => setBody(event.target.value)} autoComplete="off" />
            <Button type="submit">Add</Button>
          </div>
        </form>
      </Card>
      <ul className="flex flex-col gap-2">
        {notes.map((note, i) => (
          <li key={i} className="rounded-md border px-4 py-3 text-sm break-words">
            {note}
          </li>
        ))}
      </ul>
    </main>
  );
}
