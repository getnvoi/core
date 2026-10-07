/**
 * The one worker in front of every preview, and a transport.
 *
 * It holds no provider key and no binding to a substrate. It asks the app where
 * this preview lives, gets a host and a set of headers, and sets them verbatim —
 * so it never learns a vendor's header names, and moving a box from one
 * substrate to another reaches it as a different answer rather than a deploy.
 *
 *     GET  /route   where to send this request      cached, then throttled
 *     POST /touch   somebody is looking             throttled per hostname
 *     POST /boot    is it running; `?start=1` brings it back    every document
 *
 * They are asked on `sandbox.nvoi.to`, which carries these three and nothing
 * else — the app builds that host into the match, so the hostname is the prefix
 * and a path prefix would only repeat it.
 *
 * Auth is offline and first: a forged or missing token is refused here, with no
 * call to the app and nothing woken. That is what stops anything sweeping the
 * zone from costing us work.
 *
 * Every throttle here is this worker's own restraint, held in the Workers cache
 * and therefore per colo. The app carries a ceiling of its own, keyed on the
 * worktree, and answers 429 when one is reached — so a caller that skips the
 * edge skips these bounds and not that one.
 *
 * One path on every preview host is ours: `/__nvoi/pty/<id>`, a socket onto the
 * machine's shells (see `terminal` below).
 *
 * An artifact is a preview like any other: its box is the artifact, its route a
 * port on the worktree's machine. When the token names a viewer and the route
 * carries a key, a signed `x-nvoi-viewer` header tells the app who is looking.
 */
import { gate, isDocument, seal, terminalGate, type GateResult } from "./gate.ts";
import { busy, halted, progress, stopped, type Step } from "./shell.ts";

interface Env {
  GATE_JWT_SECRET: string;
  /** Where the app answers. */
  APP_URL: string;
  /** Hosts that are the app itself and must go to origin, comma separated. */
  RESERVED_HOSTS: string;
  /** Origins of the app pages that frame a preview, comma separated. */
  PARENT_ORIGINS: string;
}

/**
 * Where a preview lives right now, in whatever terms its substrate uses.
 *
 * `scheme` is the app's to say: a sandbox substrate fronts its ports with TLS,
 * a plain VM answers HTTP on its address and port. Absent, it is https.
 */
type Route = {
  host: string;
  scheme?: string;
  token: string | null;
  headers: Record<string, string>;
  /** The key the viewer header is signed with, when the app reads one. The app holds the same. */
  viewer_key?: string;
};

/** Who is looking, signed. Only this worker may set it. */
const VIEWER = "x-nvoi-viewer";

/** The app says this hostname belongs to nothing. Distinct from "not up yet". */
const GONE = "gone";

/** The app's own ceiling, reached. Distinct from a preview that is not up. */
const BUSY = "busy";

const DOWN = new Set([502, 503, 504, 521, 522, 523, 525, 530]);

// A parked Daytona box answers 400 with a sentence in the body, so the status
// alone cannot tell it from a real bad request.
const PARKED = /Is the Sandbox started|resolve container IP/i;

// A box that is gone, said as a 404 carrying the substrate's own JSON. The
// status alone cannot tell it from a page the app does not have, so this reads
// the body the same way PARKED does — and getting it wrong in the other
// direction would swallow every broken link in somebody's app.
//
// Proxied through, this shows a viewer their provider's error text, its sandbox
// id and its internal path, for a preview that only needed booting.
const NO_SANDBOX = /not found: sandbox\b|sandbox .* not found/i;

/** How long a resolved route may be reused, in seconds. */
const ROUTE_TTL = 30;
/** How often one hostname may stamp activity, in seconds. */
const TOUCH_EVERY = 60;

// A resolve that came back with no route, remembered in seconds for long
// enough to stop a loop and not long enough for a viewer to notice.
//
// Short because the wait page refreshes at five seconds and a box can come up
// inside one window: hold the answer longer and a preview that is already back
// keeps showing a page that says it is coming. Nothing else bounds this call —
// a route is cached only when there is one to cache — so without this a parked
// preview costs one resolve per request, and each of those does substrate work.
const MISS_EVERY = 5;
const REACH_TIMEOUT_MS = 8_000;

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(request.url);

    // The app's own hosts are not previews. First, and cheapest.
    if (reserved(url.hostname, env)) return fetch(request);

    // Before the gate: the script holds no secret.
    if (url.pathname === BRIDGE) return bridge(env);

    try {
      if (url.pathname.startsWith(PTY)) return await terminal(request, env, url, ctx);
      return await preview(request, env, url, ctx);
    } catch (error) {
      // Logged, and called a failure. Answering with the "starting" page here
      // would be worse than useless: it self-refreshes, so a defect in this
      // worker becomes a tab hammering it forever while the error is dropped.
      console.error(`[preview] ${url.hostname} failed:`, error);
      return problem(url.hostname);
    }
  },
};

async function preview(
  request: Request,
  env: Env,
  url: URL,
  ctx: ExecutionContext,
): Promise<Response> {
  // Only `viewer` is read from the claims. The gate checks the token's shape
  // and this forwards it verbatim; `box` names the worktree or artifact and `GateAuth`
  // is what reads it, on every call the token authenticates.
  const { claims, token, response } = await gate(request, env.GATE_JWT_SECRET);
  if (response) return response;

  // Per route, because the key comes with the route and a retry resolves anew.
  const viewer = (route: Route) => stamp(claims, route);

  // `?start=1` is a person's press on the stopped or failed page, and reaches
  // the server as such. Ours, so the app never sees it once it is up.
  const start = url.searchParams.get("start") === "1";
  url.searchParams.delete("start");

  let route = await resolveRoute(env, url.hostname, token as string, false);

  // A hostname nobody claims and a checkout that is asleep are not the same
  // answer. Collapsed into one 404, a parked preview reads as one that does not
  // exist.
  if (route === GONE) return new Response("nothing here\n", { status: 404 });

  // The app's ceiling, reached. A document gets a page that does not refresh
  // itself: this request is refused for coming too often, and a page that comes
  // back on a timer is the same fault wearing a delay. `retry-after` is the
  // window the worker will not ask inside anyway.
  if (route === BUSY) {
    const seconds = String(MISS_EVERY);

    return isDocument(request)
      ? busy(url.hostname)
      : new Response("too many requests\n", { status: 429, headers: { "retry-after": seconds } });
  }

  if (!route) return unreachable(env, request, url, token as string, start);

  // After the gate, so a stranger rattling the door is not activity.
  touch(ctx, env, url.hostname, token as string);

  if (upgrading(request)) {
    // The sandbox vendors' edges refuse any WebSocket that carries an Origin, and a browser always sends
    // one. It is checked here instead, against the page's own host, and not passed on.
    const origin = request.headers.get("origin");
    if (origin && origin !== `https://${url.hostname}`) {
      return new Response("origin not allowed\n", { status: 403 });
    }

    const opened = await handshake(request, url, route, ["origin"], await viewer(route));
    return opened ?? unreachable(env, request, url, token as string, start);
  }

  // A second readable body, kept for the retry below.
  //
  // `passthrough` sends `request.body`, which is a stream: the first attempt
  // consumes it, and re-reading it for the retry sends nothing. So a POST to a
  // preview whose token rotated got a wake page rather than the replay that
  // handling exists for. `clone()` is what gives two readers of one body, and it
  // must be taken BEFORE the first read.
  const replay = request.method === "GET" || request.method === "HEAD" ? request : request.clone();

  let answered = await proxy(request, url, route, false, await viewer(route));

  // A stale route, not the app saying no.
  //
  // The substrate's preview token ROTATES when a box parks and comes back, and
  // its edge refuses the old one with a 401 — which is indistinguishable, from
  // here, from an app protecting a page. So it is resolved AFRESH and tried
  // once more; a second 401 is the app's own and is passed through.
  //
  // Without the re-resolve a woken box serves 401 to every request until the
  // cache expires, and a full reload reads as "it never came back".
  if (answered.stale) {
    await forget(url.hostname);
    const fresh = await resolveRoute(env, url.hostname, token as string, true);

    if (fresh && fresh !== GONE && fresh !== BUSY) {
      answered = await proxy(replay, url, fresh, true, await viewer(fresh));
      route = fresh;
    }
  }

  if (answered.down) {
    // First, and always. What is cached is already wrong, and the resolve after
    // a wake would otherwise hand back the token from before it.
    await forget(url.hostname);
    return unreachable(env, request, url, token as string, start);
  }

  return bridged(request, framable(answered.response as Response));
}

// ── the terminal ────────────────────────────────────────────────────────────

/** Ours on every preview host: `<PTY><id>` is a socket onto one shell. */
const PTY = "/__nvoi/pty/";
const SHELL = /^[A-Za-z0-9_-]{1,64}$/;

/**
 * A socket onto the machine's shells, forwarded as `/pty/<id>` to the shell
 * server on the machine.
 *
 * The route is the app's answer for a terminal token, cached apart from the
 * preview's: it names another port and carries the machine's key, which must
 * never be handed to a preview request. The visitor's cookies stay here, since
 * the shell server wants none of them and the preview's is a credential.
 *
 * Nothing is booted from here. Opening the Terminal view already started the
 * machine, and the page asks again with a fresh token until it is up.
 */
async function terminal(
  request: Request,
  env: Env,
  url: URL,
  ctx: ExecutionContext,
): Promise<Response> {
  const { token, response } = await terminalGate(request, env.GATE_JWT_SECRET, parents(env));
  if (response) return response;

  const id = url.pathname.slice(PTY.length);
  if (!SHELL.test(id)) return new Response("bad shell id\n", { status: 400 });
  if (!upgrading(request)) return new Response("a terminal is a websocket\n", { status: 426 });

  const scope = `terminal:${url.hostname}`;
  const onward = new URL(url);
  onward.pathname = `/pty/${id}`;
  onward.searchParams.delete("token");

  for (const fresh of [false, true]) {
    const route = await resolveRoute(env, scope, token as string, fresh);
    if (route === GONE) return new Response("no machine here\n", { status: 404 });
    if (route === BUSY) {
      return new Response("too many requests\n", { status: 429, headers: { "retry-after": String(MISS_EVERY) } });
    }
    if (!route) break;

    if (!fresh) touch(ctx, env, scope, token as string);
    // Origin was checked by terminalGate; passed on, the vendor's edge refuses the app's page as cross-origin.
    const opened = await handshake(request, onward, route, ["cookie", "x-nvoi-token", "origin"]);

    // Their edge refusing a token that rotated across a park: resolved afresh, once.
    if (opened && (opened.status === 401 || opened.status === 403) && !fresh) {
      await forget(scope);
      continue;
    }
    if (opened && !DOWN.has(opened.status)) return opened;

    await forget(scope);
    break;
  }

  return new Response("starting up\n", { status: 503, headers: { "retry-after": "3" } });
}

/**
 * Where this preview lives, asked of the app and cached per hostname.
 *
 * A page is one document and thirty subresources; a round trip per asset would
 * put the app back on the hot path this gate exists to keep it off.
 */
async function resolveRoute(
  env: Env,
  host: string,
  token: string,
  fresh: boolean,
): Promise<Route | typeof GONE | typeof BUSY | null> {
  if (!fresh) {
    const hit = await caches.default.match(key(host));
    if (hit) return (await hit.json()) as Route;

    // One resolve per window for a hostname that has no route to cache. The
    // answer is held rather than the fact of having asked, so a viewer inside
    // the window is told the same thing the app said.
    const held = await recent(host);
    if (held) return held === BUSY ? BUSY : null;
  }

  try {
    const answered = await fetch(`${env.APP_URL}/route`, {
      headers: { "x-nvoi-token": token },
      signal: AbortSignal.timeout(3_000),
    });

    // The app is the authority on whether this token is still current.
    if (answered.status === 401) {
      await forget(host);
      return hold(host, null);
    }

    // Not held either way: a preview turned off and straight back on would
    // otherwise stay refused for the length of a window, and this answer costs
    // the app a row read and no substrate call. Only a verified token reaches
    // here.
    if (answered.status === 404 || answered.status === 410) return GONE;

    if (answered.status === 429) return hold(host, BUSY);
    if (!answered.ok) return hold(host, null);

    const route = (await answered.json()) as Route;
    await remember(host, route);
    return route;
  } catch (error) {
    // Named, because these are two different failures wearing one answer: the
    // app being unreachable, and a 200 whose body is not a route. The first is
    // an outage and `unreachable/5` is right to ask for a boot; the second is
    // our bug, and silence is how it stays one.
    console.error(`[preview] could not resolve a route for ${host}:`, error);
    return hold(host, null);
  }
}

/** The key under which a resolve that produced no route is held. */
function miss(host: string): Request {
  return new Request(`https://nvoi-miss.internal/${host}`);
}

async function recent(host: string): Promise<typeof BUSY | "down" | undefined> {
  try {
    const hit = await caches.default.match(miss(host));
    return hit ? ((await hit.text()) as typeof BUSY | "down") : undefined;
  } catch {
    // A cache we could not read is a round trip we will make again.
    return undefined;
  }
}

/** Remember what the app said, and hand the same answer back. */
async function hold(host: string, answer: typeof BUSY | null): Promise<typeof BUSY | null> {
  try {
    await caches.default.put(
      miss(host),
      new Response(answer ?? "down", {
        headers: { "cache-control": `max-age=${MISS_EVERY}` },
      }),
    );
  } catch {
    // Same.
  }

  return answer;
}

function key(host: string): Request {
  return new Request(`https://nvoi-route.internal/${host}`);
}

async function remember(host: string, route: Route): Promise<void> {
  try {
    await caches.default.put(
      key(host),
      new Response(JSON.stringify(route), {
        headers: { "content-type": "application/json", "cache-control": `max-age=${ROUTE_TTL}` },
      }),
    );
  } catch {
    // A cache we could not write is a round trip we will make again.
  }
}

async function forget(host: string): Promise<void> {
  try {
    await caches.default.delete(key(host));
  } catch {
    // Same.
  }
}

/**
 * Somebody is looking, so say so.
 *
 * `waitUntil`, so a visitor never waits on it, and throttled through the cache
 * so a page and its thirty assets are one call a minute. The cache is per colo,
 * so a preview read from three continents stamps three times a window — the
 * right kind of wrong, since three answers of "a person was here" agree.
 */
function touch(ctx: ExecutionContext, env: Env, host: string, token: string): void {
  ctx.waitUntil(
    (async () => {
      try {
        const stamp = new Request(`https://nvoi-touch.internal/${host}`);
        if (await caches.default.match(stamp)) return;

        // Written BEFORE the call: two requests arriving together would
        // otherwise both miss and both touch.
        await caches.default.put(
          stamp,
          new Response("", { headers: { "cache-control": `max-age=${TOUCH_EVERY}` } }),
        );

        await fetch(`${env.APP_URL}/touch`, {
          method: "POST",
          headers: { "x-nvoi-token": token },
          signal: AbortSignal.timeout(5_000),
        });
      } catch {
        // An idle clock we failed to reset costs a wake, not a wrong answer.
      }
    })(),
  );
}

/**
 * Both ways an origin turns out to be unreachable end here: no route at all, and
 * a route that answered like a parked box.
 *
 * A document is somebody looking, and gets a page. Everything else is an
 * asset: a page in place of a stylesheet helps nobody, and an asset never
 * starts anything — only a person's press does.
 */
async function unreachable(
  env: Env,
  request: Request,
  url: URL,
  token: string,
  start: boolean,
): Promise<Response> {
  if (!isDocument(request)) {
    return new Response("starting up\n", { status: 503, headers: { "retry-after": "3" } });
  }

  // Awaited here, because it answers with the state. Boot replies 202 before
  // the box is up — the work outlives the request — so this costs one round
  // trip and buys a page that names what is happening. Without `start` it
  // starts nothing: it only reports.
  //
  // The edge holds no provider key: it asks the app, which owns the substrate
  // and coalesces.
  const boot = await plan(env, token, start);

  // Coming up on its own, so the page may wait on a timer.
  if (boot.state === "starting" || boot.state === "running") {
    return starting(url, token, boot.steps);
  }

  // A run that died is not coming back on its own, so the page must not ask
  // again on a timer. It says why, and the one press is the retry.
  if (boot.state === "failed" && boot.failure) return failed(url, token, boot.failure);

  return notRunning(url, token);
}

/** Why the app is not up, as the server names it. `cause` is what the run's last lines said. */
type Failure = {
  process: string;
  exit_code: number | null;
  cause: "host_capacity" | null;
  /** Why the box did not start, for `box`; the substrate's own words. */
  message: string | null;
};

type Boot = {
  state: "starting" | "stopped" | "failed" | "running";
  steps: Step[];
  failure: Failure | null;
};

/**
 * Where the preview is, as the app says: stopped, coming up, running, or died.
 * With `start` the app starts it and answers "starting"; without, it only
 * reports. An empty list is a page without steps, not a failure.
 *
 * An answer that is not ok or not JSON is read as stopped: nothing is coming
 * on its own, and the page must not say it is.
 */
async function plan(env: Env, token: string, start: boolean): Promise<Boot> {
  const nothing: Boot = { state: "stopped", steps: [], failure: null };

  try {
    const answered = await fetch(`${env.APP_URL}/boot${start ? "?start=1" : ""}`, {
      method: "POST",
      headers: { "x-nvoi-token": token },
      signal: AbortSignal.timeout(5_000),
    });

    if (!answered.ok) return nothing;

    const body = (await answered.json()) as Partial<Boot>;
    return { state: body.state ?? "stopped", steps: body.steps ?? [], failure: body.failure ?? null };
  } catch {
    return nothing;
  }
}

// ── the proxy ───────────────────────────────────────────────────────────────

function target(url: URL, route: Route): string {
  return `${route.scheme ?? "https"}://${route.host}${url.pathname}${url.search}`;
}

/**
 * What the origin needs, and this script does not know what it is.
 *
 * The route carries a map the app built from whichever substrate owns the box.
 * Every spelling a vendor wants lives there and none of it lives here — a token
 * in the wrong header is a 403 that reads as a preview which is simply down.
 */
function authorise(headers: Headers, route: Route): Headers {
  for (const [name, value] of Object.entries(route.headers ?? {})) {
    // A cookie the origin wants joins the visitor's own; anything else replaces.
    if (name.toLowerCase() === "cookie") {
      const own = headers.get("cookie");
      headers.set("cookie", own ? `${own}; ${value}` : value);
    } else {
      headers.set(name, value);
    }
  }
  return headers;
}

/**
 * `drop` names the visitor's headers the origin must not see. The viewer header
 * is always dropped, since one sent by the visitor is a claim nobody signed.
 */
function passthrough(
  request: Request,
  route: Route,
  drop: string[] = [],
  viewer?: string,
): RequestInit {
  const headers = new Headers(request.headers);
  for (const name of [...drop, VIEWER]) headers.delete(name);

  // The address the page is on. The origin is reached on the vendor's host, so without it an app that
  // checks a form's Origin against its own URL (React Router, Rails) refuses every submission. Some
  // vendors' edges rewrite X-Forwarded-Host with their own, so it goes again in a header of ours, which
  // a visitor cannot set: it is always overwritten here.
  const host = new URL(request.url).hostname;
  headers.set("x-forwarded-host", host);
  headers.set("x-forwarded-proto", "https");
  headers.set("x-nvoi-host", host);

  authorise(headers, route);
  if (viewer) headers.set(VIEWER, viewer);

  return {
    method: request.method,
    headers,
    body: request.body,
    redirect: "manual",
  };
}

/**
 * The connect is bounded and the socket it opens is not, so the timer is
 * cancelled once the origin answers — `AbortSignal.timeout` would keep counting
 * over the live conversation and cut it at `REACH_TIMEOUT_MS`. Null is an origin
 * that never answered. The request is not rebuilt: a constructed one may not
 * carry `Upgrade`, and the origin answers 426 to every WebSocket.
 */
async function handshake(
  request: Request,
  url: URL,
  route: Route,
  drop: string[] = [],
  viewer?: string,
): Promise<Response | null> {
  const connect = new AbortController();
  const bound = setTimeout(() => connect.abort(), REACH_TIMEOUT_MS);

  const init = passthrough(request, route, drop, viewer);
  init.signal = connect.signal;

  try {
    return await fetch(target(url, route), init);
  } catch {
    return null;
  } finally {
    clearTimeout(bound);
  }
}

async function proxy(
  request: Request,
  url: URL,
  route: Route,
  retried = false,
  viewer?: string,
): Promise<{ down: boolean; stale?: boolean; response?: Response }> {
  // Never onward to the origin: the token is ours, not theirs.
  const clean = new URL(url);
  clean.searchParams.delete("token");

  const init = passthrough(request, route, [], viewer);
  init.signal = AbortSignal.timeout(REACH_TIMEOUT_MS);
  if (request.method === "GET" || request.method === "HEAD") delete init.body;

  let response: Response;

  try {
    response = await fetch(target(clean, route), init);
  } catch {
    return { down: true };
  }

  if (DOWN.has(response.status)) return { down: true };

  // Their edge, refusing a token that rotated. `retried` marks the second
  // attempt so one stale route cannot loop.
  if ((response.status === 401 || response.status === 403) && !retried) {
    return { down: false, stale: true };
  }

  if (response.status === 400 || response.status === 404) {
    const body = await response.text();
    const pattern = response.status === 400 ? PARKED : NO_SANDBOX;

    if (pattern.test(body)) return { down: true };

    // Read once, handed back whole. The body is consumed to test it, so the
    // response the viewer gets is rebuilt from that text rather than from a
    // stream that has already been drained.
    return {
      down: false,
      response: new Response(body, { status: response.status, headers: response.headers }),
    };
  }

  return { down: false, response };
}

// ── the pages ───────────────────────────────────────────────────────────────

function problem(host: string): Response {
  // Our fault, and still the same screen. A person watching a list does not stop
  // being that person because the failure is ours.
  //
  // No refresh, and that is the whole difference from `starting`. The caller's
  // own comment says it: answering a worker defect with a self-refreshing page
  // turns one bug into a tab hammering it forever while the error is dropped —
  // and this page WAS refreshing, at ten seconds, doing that slowly.
  //
  // A wait screen refreshes because it is waiting for something that is coming.
  // Nothing is coming here: the request threw, and it will throw again.
  return progress(host, [], {
    title: `Something went wrong reaching ${host}`,
    detail: "It is our fault, not the app's — it has been logged. Reload to try again.",
    refresh: null,
  });
}

// The refresh names a URL carrying the token rather than the page's own. The
// page is served at the clean URL the gate 302'd to, so a bare refresh depends
// on the cookie — which a browser without partitioned cookies never sends from
// a cross-site iframe, and the frame sits on a 401 until the outer page is
// reloaded. With the token the refresh authorises itself, and the gate strips
// it again as it does for any first load.
function starting(url: URL, token: string, steps: Step[]): Response {
  const again = new URL(url);
  again.searchParams.set("token", token);

  return progress(url.hostname, steps, {
    title: "Starting this page…",
    detail:
      `${url.hostname} stops when nothing uses it, and is starting again. ` +
      "This page refreshes itself.",
    refresh: 5,
    refreshTo: again.pathname + again.search,
  });
}

// The press carries the token like the refresh does, and `start=1` reaches
// the server.
function press(url: URL, token: string): string {
  const again = new URL(url);
  again.searchParams.set("token", token);
  again.searchParams.set("start", "1");
  return again.pathname + again.search;
}

// Nothing is coming: a page load starts nothing, and the one press does.
function notRunning(url: URL, token: string): Response {
  return stopped(url.hostname, press(url, token));
}

// No refresh: the run died, and asking again every window would start a dead
// command for ever while the page said "Starting…".
function failed(url: URL, token: string, failure: Failure): Response {
  const detail =
    failure.process === "box"
      ? `The machine for this page could not be started. ${failure.message ?? ""}`.trim()
      : failure.cause === "host_capacity"
        ? "The container host is out of capacity — not your code. It usually clears in a few minutes."
        : failure.message
          ? `The app did not come up: ${failure.message}. Its log is in the session.`
          : `\`${failure.process}\` exited ${failure.exit_code ?? "without a code"}. Open its log in the session to see why.`;

  return halted({
    title: "This page did not start",
    detail: `${url.hostname}: ${detail}`,
    action: { href: press(url, token), label: "Try again" },
  });
}

// ── the bridge ──────────────────────────────────────────────────────────────

// The page around a preview cannot see into it: the preview is another origin. Every HTML page the
// preview serves loads this script, which talks to the app's page only.
//
// Out, nvoi:url: the path, the title, and whether the frame can go back or forward, on start, on load,
// on each history change, and in answer to nvoi:ping (with its id), which is how the app learns a page
// carries the script. In: nvoi:history, nvoi:navigate and nvoi:reload, which a cross-origin parent
// cannot do itself. A post whose target origin is not the parent's is dropped by the browser, so it
// goes to each allowed origin.
const BRIDGE = "/__nvoi/bridge.js";

function script(origins: string[]): string {
  return `(function(){
if (window.top === window.self) return;
var O = ${JSON.stringify(origins)};
function post(reason, id) {
  var n = window.navigation;
  var m = {type: "nvoi:url", reason: reason, id: id, title: document.title,
    path: location.pathname + location.search + location.hash,
    back: n ? n.canGoBack : history.length > 1, forward: n ? n.canGoForward : false};
  O.forEach(function (o) { try { parent.postMessage(m, o); } catch (e) {} });
}
post("start");
addEventListener("load", function () { post("load"); });
addEventListener("popstate", function () { post("popstate"); });
addEventListener("hashchange", function () { post("hashchange"); });
var push = history.pushState, replace = history.replaceState;
history.pushState = function () { push.apply(this, arguments); post("pushState"); };
history.replaceState = function () { replace.apply(this, arguments); post("replaceState"); };
addEventListener("message", function (e) {
  if (O.indexOf(e.origin) < 0 || !e.data) return;
  var d = e.data;
  if (d.type === "nvoi:history" && typeof d.delta === "number") history.go(d.delta);
  if (d.type === "nvoi:navigate" && typeof d.path === "string" && d.path.charAt(0) === "/") location.assign(d.path);
  if (d.type === "nvoi:reload") location.reload();
  if (d.type === "nvoi:ping") post("ping", d.id);
});
})();
`;
}

/** The app's origins: the pages that frame a preview and open a terminal. */
function parents(env: Env): string[] {
  return (env.PARENT_ORIGINS ?? "")
    .split(",")
    .map((origin) => origin.trim())
    .filter(Boolean);
}

function bridge(env: Env): Response {
  return new Response(script(parents(env)), {
    headers: { "content-type": "text/javascript; charset=utf-8", "cache-control": "no-cache" },
  });
}

/** The bridge's script tag, first in the head of an HTML document. */
function bridged(request: Request, response: Response): Response {
  const type = (response.headers.get("content-type") ?? "").toLowerCase();
  if (response.webSocket || !isDocument(request) || !type.includes("text/html")) return response;

  let done = false;
  return new HTMLRewriter()
    .on("head", {
      element(head) {
        if (done) return;
        done = true;
        head.prepend(`<script src="${BRIDGE}"></script>`, { html: true });
      },
    })
    .transform(response);
}

// ── small things ────────────────────────────────────────────────────────────

function reserved(hostname: string, env: Env): boolean {
  return (env.RESERVED_HOSTS ?? "")
    .split(",")
    .map((host) => host.trim())
    .filter(Boolean)
    .includes(hostname);
}

/**
 * The viewer header for this route, or nothing. A public link's visitor has no
 * viewer, and a route without a key has nothing to sign with.
 */
async function stamp(claims: GateResult["claims"], route: Route): Promise<string | undefined> {
  if (!claims.viewer || !route.viewer_key || !claims.exp) return undefined;

  return seal(claims.viewer, claims.exp, route.viewer_key);
}

function upgrading(request: Request): boolean {
  return (request.headers.get("upgrade") ?? "").toLowerCase() === "websocket";
}

/**
 * The preview renders inside our app, on another origin, so the origin's own
 * framing rules would refuse it. `x-frame-options` goes; from the CSP only
 * `frame-ancestors` goes, because the rest of somebody's policy is theirs.
 */
function framable(response: Response): Response {
  if (response.webSocket) return response;

  const xfo = response.headers.get("x-frame-options");
  const csp = response.headers.get("content-security-policy");

  if (!xfo && !csp) return response;

  const headers = new Headers(response.headers);
  headers.delete("x-frame-options");

  if (csp) {
    const kept = csp
      .split(";")
      .map((directive) => directive.trim())
      .filter((directive) => directive && !/^frame-ancestors\b/i.test(directive))
      .join("; ");

    if (kept) headers.set("content-security-policy", kept);
    else headers.delete("content-security-policy");
  }

  // The body passes by reference — not read, not buffered. A preview streams.
  return new Response(response.body, {
    status: response.status,
    statusText: response.statusText,
    headers,
  });
}
