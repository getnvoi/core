
import type { Route } from "./+types/api.jobs";
import { getAuthenticatedUser } from "~/services/auth.server";
import { isJobType } from "~/jobs";
import { connectQueue, QUEUE } from "~/services/queue.server";
import { getDb, jobs } from "~/db";

export async function action({ request }: Route.ActionArgs) {
  const user = await getAuthenticatedUser(request);
  // TYPED AT THE BOUNDARY. `request.json()` is `unknown` by design — it is
  // whatever the client sent. Naming the shape here is the assertion that this
  // is what we require; it was destructured untyped, so every field was `any`
  // and a client sending nothing typechecked fine.
  const { type, key, payload } = (await request.json()) as {
    type: string;
    key: string;
    payload: Record<string, unknown>;
  };

  // REJECTED HERE, not three failed attempts later in a worker. `type` comes
  // from a browser, so it is a claim; `JOBS` is the list of things that exist.
  if (!isJobType(type)) {
    return Response.json({ error: `unknown job type: ${type}` }, { status: 422 });
  }

  const jobId = crypto.randomUUID();

  getDb()
    .insert(jobs)
    .values({
      id: jobId,
      userId: user.id,
      type,
      key: `${user.id}:${key}`,
      status: "queued",
      payload: JSON.stringify(payload),
    })
    ;

  // NOTHING IS ANNOUNCED HERE. The row above IS the announcement: the server's
  // poller sees it and sends it to this user's open sockets, exactly as it will
  // for `started` and `completed` from the worker. Pushing it from this handler
  // too would mean two paths delivering one job, and the browser would have to
  // dedupe them.
  const boss = await connectQueue();
  await boss.send(QUEUE, { type, jobId, to: payload.to as string | undefined });

  return { success: true, jobId };
}
