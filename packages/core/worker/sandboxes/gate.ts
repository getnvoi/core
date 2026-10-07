/**
 * The preview gate: who may look at a worktree's app.
 *
 * ── WHY THIS IS AT THE EDGE AND NOT IN THE SERVER ────────────────────────────
 *
 * A preview is browser traffic — documents, assets, HMR sockets — and it must be
 * refused before it reaches a container. The server is not in that path and putting
 * it there would mean proxying every asset of every preview through the app.
 *
 * So the token is verified OFFLINE, with a secret the server is the only signer of.
 * No round trip on the hot path, and a bad token is refused before anything is
 * woken or paid for.
 *
 * ── WHAT THE TOKEN SAYS ──────────────────────────────────────────────────────
 *
 *     { aud: "preview", host, box, port, exp }
 *
 * `aud` is what makes this a preview token rather than any other thing the server
 * signs with the same secret, such as a terminal token.
 *
 * It may carry `viewer`, the workspace member looking; an artifact's public
 * link carries none.
 *
 * `host` binds a token to one preview hostname, so one leaked link does not open
 * every worktree.
 *
 * `box` is the sandbox's name. It is CARRIED rather than parsed out of the
 * hostname, because the hostname is a human-readable slug and a sandbox is named
 * by worktree id — and resolving one to the other would mean asking the server on
 * every asset request, which is the round trip this gate exists to avoid.
 *
 * `port` is where the app listens inside that box. The gate must not guess it: a
 * preview is a different port per repository, and a default would proxy one app's
 * request at another's server, or at our own agent.
 *
 * ── A TERMINAL TOKEN IS ANOTHER THING ────────────────────────────────────────
 *
 *     { aud: "terminal", host, box, exp }
 *
 * It opens one socket onto the machine's shells, at `/__nvoi/pty/<id>`, and is
 * checked by `terminalGate` alone. Its own audience, so a preview link cannot
 * open a shell and a terminal token is refused on every preview path. It is
 * read from the query or a header, never from a cookie, and never set as one:
 * a cookie on the preview host would be sent with every preview request.
 *
 * ── THE TOKEN LEAVES THE URL IMMEDIATELY ─────────────────────────────────────
 *
 * A document load carrying `?token=` gets a cookie and a 302 to the clean URL.
 * A token in an address bar ends up in history, in a Referer header, and in
 * whatever the page links to next.
 */
import { errors, jwtVerify } from "jose";

import { expired, refused } from "./shell.ts";

// An alias rather than an interface: only an alias picks up the implicit index
// signature that lets jose's `JWTPayload` be narrowed to it.
type Claims = {
  /** The worktree's preview hostname. Binds one token to one preview. */
  host?: string;
  /** The sandbox's NAME, which is the worktree id. */
  box?: string;
  /** Where the app listens inside that box. */
  port?: number;
  /** The member looking. Absent for a visitor of a public link. */
  viewer?: Viewer;
  exp?: number;
};

export type Viewer = { id: number | string; email: string; name: string };

const COOKIE = "nvoi_token";
const AUDIENCE = "preview";
const TERMINAL = "terminal";

/** A token that verified in every way but `exp`. */
const EXPIRED = "expired";

// Two machines, two clocks. `Gate` allows the same, so a token
// minted at the boundary is not a 401 neither side did anything wrong to earn.
const CLOCK_TOLERANCE = 60;

export interface GateResult {
  claims: Claims;
  /** The verified token, verbatim. The app is asked with it. */
  token?: string;
  /** Set when the request should be answered directly rather than proxied. */
  response?: Response;
}

/**
 * Decide whether this request may reach a container, and on which port.
 *
 * Returns a `response` when the answer is "not like this" — a 401, or the 302
 * that moves a token out of the URL and into a cookie.
 */
export async function gate(request: Request, secret: string): Promise<GateResult> {
  const url = new URL(request.url);
  const fromQuery = url.searchParams.get("token");
  const cookies = parseCookie(request.headers.get("Cookie") ?? "");

  // The first one that verifies wins — not the first one that exists.
  //
  // `?token=` is not ours to reserve. It is a query parameter on somebody
  // else's app, and Vite's HMR client uses it for its own short-lived token:
  //
  //     wss://<worktree>.nvoi.to/?token=wUrmAuNNMjC0
  //
  // Preferring whichever arrives first lets Vite's token shadow our cookie: it
  // fails to verify, every HMR socket is refused 401 at the edge, and the page
  // that opened it loads perfectly — a dead dev server behind a healthy-looking
  // tab, which reads as a broken WebSocket transport.
  //
  // Order still matters where several verify: a fresh link must beat the cookie
  // a browser is already holding, or a re-shared preview would keep opening with
  // the old token until it expired.
  let presented: string | null = null;
  let claims: Claims | null = null;
  // One of the candidates was ours and is past its expiry.
  let stale = false;

  for (const candidate of [fromQuery, request.headers.get("x-nvoi-token"), cookies[COOKIE]]) {
    if (!candidate) continue;

    const verified = await verify(candidate, secret);

    if (verified === EXPIRED) {
      stale = true;
      continue;
    }

    if (verified) {
      presented = candidate;
      claims = verified;
      break;
    }
  }

  // HOST-BOUND. A token that verifies but was minted for another worktree is
  // refused, so a shared link opens one preview rather than all of them.
  if (!claims || claims.host !== url.hostname) {
    return { claims: {}, response: unauthorized(request, url.hostname, stale && !claims) };
  }

  if (!claims.box) {
    return { claims, token: presented ?? undefined, response: json({ error: "this token names no sandbox" }, 400) };
  }

  if (!Number.isInteger(claims.port)) {
    // The server mints this. A token without a port means the app's port was never
    // reported, and guessing one would proxy to whatever happened to be
    // listening — including our own agent.
    return { claims, token: presented ?? undefined, response: json({ error: "this preview has no port yet" }, 503) };
  }

  // OURS, not merely present. Redirecting on any `?token=` would strip a
  // parameter belonging to the app and bounce a request that was already
  // authorised by cookie — deleting somebody else's query string on the way.
  if (presented === fromQuery && fromQuery && isDocument(request)) {
    const clean = new URL(url);
    clean.searchParams.delete("token");

    return {
      claims,
      token: presented ?? undefined,
      response: new Response(null, {
        status: 302,
        headers: {
          Location: clean.pathname + clean.search + clean.hash,
          // SameSite=None, and it has to be.
          //
          // A preview is displayed as a CROSS-SITE iframe inside the app. `Lax`
          // is not sent on embedded requests, so the document loads — it carried
          // `?token=` — and then every asset it asks for arrives with no
          // credential and is refused 401. The page renders half-broken while
          // the gate looks perfectly correct in isolation.
          //
          // `Partitioned` because Chrome now requires it for third-party
          // cookies: without it the cookie is dropped entirely rather than
          // merely restricted, which is the same symptom with a different cause.
          //
          // A test names this header verbatim, because it is a line that is
          // easy to lose in a refactor and impossible to miss the loss of.
          "Set-Cookie":
            `${COOKIE}=${presented}; Path=/; HttpOnly; Secure; SameSite=None; Partitioned; Max-Age=86400`,
        },
      }),
    };
  }

  return { claims, token: presented ?? undefined };
}

/**
 * Decide whether this socket may reach the machine's shells. A `response` is a
 * refusal, and a bare status: nothing reads a page in place of a socket.
 *
 * The browser opens it from the app's pages, so an `Origin` naming anywhere
 * else is refused before the token is looked at.
 */
export async function terminalGate(
  request: Request,
  secret: string,
  parents: string[],
): Promise<GateResult> {
  const url = new URL(request.url);
  const from = request.headers.get("Origin");
  if (from && !parents.includes(from)) {
    return { claims: {}, response: new Response("origin not allowed\n", { status: 403 }) };
  }

  for (const candidate of [url.searchParams.get("token"), request.headers.get("x-nvoi-token")]) {
    const verified = await verify(candidate, secret, TERMINAL);
    if (!verified || verified === EXPIRED || verified.host !== url.hostname) continue;
    if (!verified.box) break;

    return { claims: verified, token: candidate as string };
  }

  return {
    claims: {},
    response: new Response("401 Unauthorized\n", {
      status: 401,
      headers: { "content-type": "text/plain" },
    }),
  };
}

/**
 * The `x-nvoi-viewer` header an app reads to know who is looking:
 * `<payload>.<sig>`, where the payload is base64url of the viewer and the
 * token's expiry as JSON, and the sig is base64url of its HMAC-SHA256 under the
 * route's `viewer_key`. The app checks it with the same key, so a header made
 * up by a visitor or by the app's own page does not verify.
 */
export async function seal(viewer: Viewer, exp: number, key: string): Promise<string> {
  const encoder = new TextEncoder();
  // Encoded to UTF-8 first: a name outside Latin-1 would make `btoa` throw.
  const payload = base64url(encoder.encode(JSON.stringify({ ...viewer, exp })));

  const hmac = await crypto.subtle.importKey(
    "raw",
    encoder.encode(key),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  const mac = await crypto.subtle.sign("HMAC", hmac, encoder.encode(payload));

  return `${payload}.${base64url(new Uint8Array(mac))}`;
}

function base64url(bytes: Uint8Array): string {
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);

  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/**
 * Verify a gate token against the shared secret, for one audience.
 *
 * Returns null for anything wrong — malformed, wrong signature, wrong audience —
 * and `EXPIRED` for our own token past its expiry, the one refusal a viewer can
 * act on. HS256 is pinned here rather than read from the token header, which is
 * the whole of what `alg: "none"` asks for, and `exp` is required rather than
 * merely checked when present, because the signer that always sets one is
 * another file in another language.
 */
async function verify(
  token: string | null | undefined,
  secret: string,
  audience: string = AUDIENCE,
): Promise<Claims | typeof EXPIRED | null> {
  if (!token || !secret) return null;

  try {
    const { payload } = await jwtVerify(token, new TextEncoder().encode(secret), {
      algorithms: ["HS256"],
      audience,
      clockTolerance: CLOCK_TOLERANCE,
      requiredClaims: ["exp"],
    });

    return payload as Claims;
  } catch (error) {
    // Only after the signature and the audience checked out.
    if (error instanceof errors.JWTExpired) return EXPIRED;

    // A malformed token is a 401, not a 500. The only inputs here are attacker
    // controlled, so every parse failure is an expected answer.
    return null;
  }
}

/**
 * A PERSON GETS A PAGE; EVERYTHING ELSE GETS A STATUS.
 *
 * A preview token expires in an hour, so the commonest reader of a refusal is
 * somebody returning to yesterday's tab — and `401 Unauthorized\n` in the
 * browser's default serif told them the status code and nothing they could act
 * on. `stale` is that case, and gets its own page.
 *
 * An XHR, an image and a websocket still get the bare line. A page is for
 * reading, and nothing reads one but a browser showing a document.
 */
function unauthorized(request: Request, host: string, stale: boolean): Response {
  if (isDocument(request)) return stale ? expired(host) : refused(host);

  return new Response("401 Unauthorized\n", {
    status: 401,
    headers: { "content-type": "text/plain" },
  });
}

// A document can be shown a page; an XHR, an image and a websocket cannot.
//
// Exported because the same distinction decides two things: whether a token in
// a query string may set a cookie, and whether a starting preview is answered
// with its steps or held until the app binds.
export function isDocument(request: Request): boolean {
  return (
    request.method === "GET" &&
    (request.headers.get("Sec-Fetch-Dest") === "document" ||
      (request.headers.get("Accept") ?? "").includes("text/html"))
  );
}

function parseCookie(header: string): Record<string, string> {
  const out: Record<string, string> = {};

  for (const part of header.split(";")) {
    const i = part.indexOf("=");
    if (i >= 0) out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  }

  return out;
}

function json(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
}
