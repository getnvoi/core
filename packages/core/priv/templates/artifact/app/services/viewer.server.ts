import { createHmac, timingSafeEqual } from "node:crypto";
import type { IncomingHttpHeaders } from "node:http";

// The person looking at the app, as nvoi signs them into the x-nvoi-viewer header.
export type Viewer = { id: string; email: string; name: string };

export const HEADER = "x-nvoi-viewer";

// Used when the app runs on the agent's box, where no edge signs a header.
export const DEV_VIEWER: Viewer = { id: "dev", email: "dev@example.com", name: "Developer" };

type Source = { headers: Headers | IncomingHttpHeaders };

// Reads the viewer from a Request, or from the headers of a Node upgrade request.
// Anything missing, malformed, badly signed or expired is anonymous.
export function viewer(request: Source): Viewer | null {
  const key = process.env.NVOI_VIEWER_KEY;

  if (!key) return process.env.NODE_ENV === "production" ? null : DEV_VIEWER;

  const header = read(request.headers);
  if (!header) return null;

  return verify(header, key);
}

// For loaders and actions that make no sense without a viewer.
export function requireViewer(request: Source): Viewer {
  const found = viewer(request);
  if (!found) throw new Response("Unauthorized", { status: 401 });

  return found;
}

export function verify(header: string, key: string, now = Date.now()): Viewer | null {
  const [payload, sig, extra] = header.split(".");
  if (!payload || !sig || extra !== undefined) return null;

  const expected = createHmac("sha256", key).update(payload).digest();
  const given = Buffer.from(sig, "base64url");

  if (given.length !== expected.length || !timingSafeEqual(given, expected)) return null;

  let claims: unknown;
  try {
    claims = JSON.parse(Buffer.from(payload, "base64url").toString("utf8"));
  } catch {
    return null;
  }

  if (!claims || typeof claims !== "object") return null;

  const { id, email, name, exp } = claims as Record<string, unknown>;

  if (typeof exp !== "number" || exp * 1000 <= now) return null;
  if (typeof id !== "string" && typeof id !== "number") return null;

  return { id: String(id), email: String(email ?? ""), name: String(name ?? "") };
}

// The inverse of verify, for tests and local scripts; nvoi's edge does this in production.
export function sign(viewer: Viewer, exp: number, key: string): string {
  const payload = Buffer.from(JSON.stringify({ ...viewer, exp })).toString("base64url");
  const sig = createHmac("sha256", key).update(payload).digest("base64url");

  return `${payload}.${sig}`;
}

function read(headers: Headers | IncomingHttpHeaders): string | null {
  if (headers instanceof Headers) return headers.get(HEADER);

  const value = headers[HEADER];

  return (Array.isArray(value) ? value[0] : value) ?? null;
}
