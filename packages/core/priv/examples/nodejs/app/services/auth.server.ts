import { redirect } from "react-router";
import { getSession, commitSession, destroySession } from "~/sessions.server";
import type { SessionUser } from "~/sessions.server";

// A seed starts with email auth. A project that wants OAuth adds the one
// provider it actually uses.

export async function getUserFromSession(
  request: Request,
): Promise<SessionUser | null> {
  const session = await getSession(request.headers.get("Cookie"));
  const user = session.get("user");
  return user || null;
}

export async function requireUser(request: Request): Promise<SessionUser> {
  const user = await getUserFromSession(request);
  if (!user) {
    throw redirect("/");
  }
  return user;
}

export async function getAuthenticatedUser(
  request: Request,
): Promise<SessionUser> {
  const user = await getUserFromSession(request);
  if (!user) {
    throw new Response("Unauthorized", { status: 401 });
  }
  return user;
}

export async function createUserSession(
  user: SessionUser,
  redirectTo: string,
): Promise<Response> {
  const session = await getSession();
  session.set("user", user);

  return redirect(redirectTo, {
    headers: {
      "Set-Cookie": await commitSession(session),
    },
  });
}

export async function logout(request: Request): Promise<Response> {
  const session = await getSession(request.headers.get("Cookie"));

  return redirect("/", {
    headers: {
      "Set-Cookie": await destroySession(session),
    },
  });
}

/**
 * Who a raw `Cookie` header belongs to, or nobody.
 *
 * THE WEBSOCKET UPGRADE IS NOT A `Request`. It arrives at the Node server as an
 * `IncomingMessage`, so the helpers above — which all take a fetch `Request` —
 * cannot be used, and the session cookie on it is the only thing that says who
 * is asking.
 *
 * Without this, any client could open the socket and be handed another user's
 * jobs. On Cloudflare the Durable Object was addressed by user id from an
 * already-authenticated loader, so the question never came up.
 */
export async function userIdFromRequest(
  cookie: string | undefined,
): Promise<string | null> {
  if (!cookie) return null;

  const session = await getSession(cookie);

  return session.get("user")?.id ?? null;
}
