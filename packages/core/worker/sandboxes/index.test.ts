import { beforeEach, describe, expect, it, jest } from "bun:test";

/**
 * The worker is a transport, so what is worth testing is what it forwards and what it
 * refuses. There is no substrate here and no binding to stub — every question it
 * cannot answer offline, it asks the app.
 *
 * The one thing this cannot prove is that a real origin behaves as assumed. That
 * is a real preview opened from a session.
 */
const { default: worker } = await import("./index.ts");
const { seal } = await import("./gate.ts");

const APP = "https://dev.nvoi.to";
const HOST = "wt-1-preview-dev.nvoi.to";
const SECRET = "test-gate-secret";

const env = {
  GATE_JWT_SECRET: SECRET,
  APP_URL: APP,
  RESERVED_HOSTS: "app.nvoi.to",
  PARENT_ORIGINS: "https://dev.nvoi.to, https://app.nvoi.to",
};

const ctx = { waitUntil: (work: Promise<unknown>) => void work, passThroughOnException: () => {} };

// The route the app hands back. Its header names are Daytona's; this file is the
// only place in the worker's tests they appear, which is the point.
const ROUTE = {
  host: "3000-sb-1.proxy.daytona.works",
  token: "tok-1",
  headers: { "x-daytona-preview-token": "tok-1", "x-daytona-skip-preview-warning": "true" },
};

/** The cache is per colo and has no test double, so it is one here. */
const cache = new Map<string, Response>();

let fetches: Array<{ url: string; init?: RequestInit }> = [];
let origin: () => Response | Promise<Response>;
let app: (path: string, url: URL) => Response;

/** What `/boot` answers: the state, with an empty plan unless one is given. */
const boot = (
  state: "starting" | "stopped" | "failed" | "running",
  rest: Record<string, unknown> = {},
) =>
  new Response(JSON.stringify({ steps: [], failure: null, state, ...rest }), {
    status: 202,
    headers: { "content-type": "application/json" },
  });

/** An app whose preview has no route, and answers `/boot` as given. */
const parked = (answer: (url: URL) => Response) => (path: string, url: URL) =>
  path === "/route" ? new Response(null, { status: 503 }) : answer(url);

beforeEach(() => {
  cache.clear();
  fetches = [];
  origin = () => new Response("the app", { status: 200 });
  app = (path) =>
    path === "/route"
      ? new Response(JSON.stringify(ROUTE), {
          headers: { "content-type": "application/json" },
        })
      : path === "/boot"
        ? boot("stopped")
        : new Response(null, { status: 204 });

  // Bun has no `caches` and its `fetch` would reach the network: both are
  // replaced on the global for the length of the file.
  (globalThis as { caches: unknown }).caches = {
    default: {
      // A CLONE, because the real cache hands back a fresh Response per match.
      // Returning the stored object lets one read consume it for every later one.
      match: async (request: Request) => cache.get(request.url)?.clone(),
      put: async (request: Request, response: Response) => void cache.set(request.url, response),
      delete: async (request: Request) => cache.delete(request.url),
    },
  };

  globalThis.fetch = (async (input: string | Request, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.url;
    fetches.push({ url, init });

    if (url.startsWith(APP)) return app(new URL(url).pathname, new URL(url));
    return origin();
  }) as typeof fetch;
});

// Minted by hand rather than with jose, so what the worker accepts is checked
// against bytes assembled the way `Gate` assembles them.
async function sign(claims: Record<string, unknown>, expiresIn = 3600): Promise<string> {
  // As UTF-8, the way the server writes JSON, so a viewer's name may be any text.
  const encode = (value: unknown) =>
    btoa(String.fromCharCode(...new TextEncoder().encode(JSON.stringify(value))))
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "");

  const input = `${encode({ alg: "HS256", typ: "JWT" })}.${encode({
    aud: "preview",
    ...claims,
    exp: Math.floor(Date.now() / 1000) + expiresIn,
  })}`;

  const key = await crypto.subtle.importKey(
    "raw",
    new TextEncoder().encode(SECRET),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );

  const mac = await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(input));

  const b64 = btoa(String.fromCharCode(...new Uint8Array(mac)))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");

  return `${input}.${b64}`;
}

async function open(path = "/", init: RequestInit = {}, host = HOST): Promise<Response> {
  const token = await sign({ host, box: "wt-1", port: 3000 });

  return worker.fetch(
    new Request(`https://${host}${path}`, {
      ...init,
      headers: { ...(init.headers ?? {}), "x-nvoi-token": token, accept: "text/html" },
    }),
    env as never,
    ctx as never,
  );
}

const asked = (path: string) => fetches.filter((call) => call.url === `${APP}${path}`);
const reached = () => fetches.filter((call) => call.url.includes("proxy.daytona.works"));

describe("the gate", () => {
  it("refuses an unsigned request offline, with nothing asked and nothing woken", async () => {
    const response = await worker.fetch(
      new Request(`https://${HOST}/`),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(401);
    expect(fetches).toHaveLength(0);
  });

  it("refuses a token minted for another hostname", async () => {
    const token = await sign({ host: "someone-else.nvoi.to", box: "wt-2", port: 3000 });

    const response = await worker.fetch(
      new Request(`https://${HOST}/`, { headers: { "x-nvoi-token": token } }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(401);
    expect(fetches).toHaveLength(0);
  });

  it("refuses an artifact token, which carries every claim this route reads", async () => {
    // Our signature, unexpired, and named for the other worker. The audience is
    // the only thing saying it was not minted for a preview.
    const token = await sign({ aud: "artifact", host: HOST, box: "wt-1", port: 3000 });

    const response = await worker.fetch(
      new Request(`https://${HOST}/`, { headers: { "x-nvoi-token": token } }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(401);
    expect(fetches).toHaveLength(0);
  });

  it("refuses a token whose header names another algorithm", async () => {
    // `alg: "none"` asks the verifier to take the algorithm from the token. The
    // header is signed over, so this one still carries our signature — and is
    // refused anyway, because HS256 is the verifier's and not the token's.
    const encode = (value: unknown) =>
      btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");

    const input = `${encode({ alg: "none", typ: "JWT" })}.${encode({
      aud: "preview",
      host: HOST,
      box: "wt-1",
      port: 3000,
      exp: Math.floor(Date.now() / 1000) + 3600,
    })}`;

    const key = await crypto.subtle.importKey(
      "raw",
      new TextEncoder().encode(SECRET),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign"],
    );

    const mac = new Uint8Array(
      await crypto.subtle.sign("HMAC", key, new TextEncoder().encode(input)),
    );

    const token = `${input}.${btoa(String.fromCharCode(...mac))
      .replace(/\+/g, "-")
      .replace(/\//g, "_")
      .replace(/=+$/, "")}`;

    const response = await worker.fetch(
      new Request(`https://${HOST}/`, { headers: { "x-nvoi-token": token } }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(401);
    expect(fetches).toHaveLength(0);
  });

  it("lets through a token expired by less than the clock tolerance", async () => {
    const token = await sign({ host: HOST, box: "wt-1", port: 3000 }, -10);

    const response = await worker.fetch(
      new Request(`https://${HOST}/`, {
        headers: { "x-nvoi-token": token, accept: "text/html" },
      }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(200);
  });

  it("refuses a token expired by more than the clock tolerance", async () => {
    const token = await sign({ host: HOST, box: "wt-1", port: 3000 }, -120);

    const response = await worker.fetch(
      new Request(`https://${HOST}/`, { headers: { "x-nvoi-token": token } }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(401);
    expect(await response.text()).toBe("401 Unauthorized\n");
    expect(fetches).toHaveLength(0);
  });

  it("tells a document its expired token has expired", async () => {
    // Our signature, past its time: the one refusal a person can act on.
    const token = await sign({ host: HOST, box: "wt-1", port: 3000 }, -120);

    const response = await worker.fetch(
      new Request(`https://${HOST}/`, {
        headers: { "x-nvoi-token": token, accept: "text/html" },
      }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(401);
    const html = await response.text();
    expect(html).toContain("has expired");
    expect(html).not.toContain('http-equiv="refresh"');
    expect(fetches).toHaveLength(0);
  });

  it("tells a document a token with a bad signature is not valid, not expired", async () => {
    const token = await sign({ host: HOST, box: "wt-1", port: 3000 }, -120);
    const forged = token.slice(0, -4) + (token.endsWith("AAAA") ? "BBBB" : "AAAA");

    const response = await worker.fetch(
      new Request(`https://${HOST}/`, {
        headers: { "x-nvoi-token": forged, accept: "text/html" },
      }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(401);
    const html = await response.text();
    expect(html).toContain("not valid");
    expect(html).not.toContain("has expired");
  });
});

describe("reserved hosts", () => {
  it("go to the origin without being gated", async () => {
    const response = await worker.fetch(
      new Request("https://app.nvoi.to/login"),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(200);
    expect(asked("/route")).toHaveLength(0);
  });
});

describe("routing", () => {
  it("asks the app where to go and sets the headers it is handed", async () => {
    const response = await open("/dashboard");

    expect(response.status).toBe(200);
    expect(asked("/route")).toHaveLength(1);

    const [call] = reached();
    expect(call.url).toBe("https://3000-sb-1.proxy.daytona.works/dashboard");

    const headers = new Headers(call.init?.headers as HeadersInit);
    expect(headers.get("x-daytona-preview-token")).toBe("tok-1");
    expect(headers.get("x-daytona-skip-preview-warning")).toBe("true");
  });

  it("a cookie the route hands joins the visitor's own; boat.dev's edge wants one", async () => {
    app = (path) =>
      path === "/route"
        ? new Response(
            JSON.stringify({
              host: "swift-otter-3000.on.boat.dev",
              token: "tok-b",
              headers: { cookie: "_port_auth=tok-b" },
            }),
            { headers: { "content-type": "application/json" } },
          )
        : new Response(null, { status: 204 });

    const response = await open("/", { headers: { cookie: "theirs=1" } });

    expect(response.status).toBe(200);
    const [call] = fetches.filter((f) => f.url.includes("on.boat.dev"));
    const headers = new Headers(call.init?.headers as HeadersInit);
    expect(headers.get("cookie")).toContain("theirs=1");
    expect(headers.get("cookie")).toContain("_port_auth=tok-b");
  });

  it("proxies over the scheme the route names, plain HTTP for a VM on its address", async () => {
    // A Hetzner box has no edge in front of it: the app answers HTTP on the
    // machine's address and port, with nothing to authorise.
    app = (path) =>
      path === "/route"
        ? new Response(
            JSON.stringify({ host: "1.2.3.4:3000", scheme: "http", token: null, headers: {} }),
            { headers: { "content-type": "application/json" } },
          )
        : new Response(null, { status: 204 });

    globalThis.fetch = (async (input: string | Request, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.url;
      fetches.push({ url, init });
      return url.startsWith(APP) ? app(new URL(url).pathname, new URL(url)) : new Response("the app");
    }) as typeof fetch;

    const response = await open("/dashboard");

    expect(response.status).toBe(200);
    expect(fetches.map((call) => call.url)).toContain("http://1.2.3.4:3000/dashboard");
  });

  it("caches the route, so a page and its assets are one round trip", async () => {
    await open("/");
    await open("/app.css");

    expect(asked("/route")).toHaveLength(1);
    expect(reached()).toHaveLength(2);
  });

  it("410 from the app is a preview nobody has, and is 404 to the viewer", async () => {
    app = () => new Response(null, { status: 410 });

    expect((await open("/")).status).toBe(404);
  });

  it("a hostname the app says is gone is NOT cached", async () => {
    // A preview turned off and straight back on would otherwise stay refused for
    // the length of the cache.
    app = () => new Response(null, { status: 410 });

    await open("/");
    await open("/");

    expect(asked("/route")).toHaveLength(2);
  });

  it("503 from the app is a parked checkout, and a document is told what the app says", async () => {
    app = parked(() => boot("starting"));

    const response = await open("/");

    expect(response.status).toBe(503);
    expect(await response.text()).toContain("Starting this page");
    expect(asked("/boot")).toHaveLength(1);
  });

  // A page load starts nothing. The stopped page carries the one press that does.
  it("a document with no start param asks without one, and gets the stopped page", async () => {
    app = parked(() => boot("stopped"));

    const response = await open("/?x=1");
    const html = await response.text();

    expect(response.status).toBe(503);
    expect(html).toContain("is not running");
    expect(html).not.toContain('http-equiv="refresh"');
    expect(html).toContain(">Start<");
    expect(html).toMatch(/href="[^"]*start=1/);
    expect(html).toMatch(/href="[^"]*token=/);
    expect(html).toContain("x=1");

    const [call] = asked("/boot");
    expect(call.init?.method).toBe("POST");
    expect(new URL(call.url).searchParams.has("start")).toBe(false);
  });

  it("?start=1 asks with start=1, and gets the starting page", async () => {
    app = parked((url) => boot(url.searchParams.get("start") === "1" ? "starting" : "stopped"));

    const html = await (await open("/?start=1")).text();

    expect(html).toContain("Starting this page");
    expect(html).not.toContain("\u2014");
    expect(html).not.toMatch(/container|fresh checkout/i);
    // The refresh never carries the press, or every refresh would be one.
    expect(html).toMatch(/http-equiv="refresh" content="5; url=[^"]*token=/);
    expect(html).not.toMatch(/url=[^"]*start=1/);

    const [call] = asked("/boot?start=1");
    expect(new URL(call.url).searchParams.get("start")).toBe("1");
  });

  it("an answer that is not ok is read as stopped, not as coming", async () => {
    app = parked(() => new Response("nope", { status: 500 }));

    const html = await (await open("/")).text();

    expect(html).toContain("is not running");
    expect(html).not.toContain('http-equiv="refresh"');
  });

  it("an answer that is not JSON is read as stopped", async () => {
    app = parked(() => new Response(null, { status: 202 }));

    expect(await (await open("/")).text()).toContain("is not running");
  });

  it("a preview already running is shown the wait page, with a refresh", async () => {
    app = parked(() => boot("running"));

    const html = await (await open("/")).text();

    expect(html).toContain("Starting this page");
    expect(html).toContain('http-equiv="refresh"');
  });

  // A run that died is not brought back by a page refreshing itself. The boot
  // names the failure; the page stops, says why, and offers one press.
  it("a boot answering a failed run stops the page, names the cause, and offers a retry", async () => {
    app = parked(() =>
      boot("failed", {
        steps: [{ key: "app", label: "Starting app", state: "failed" }],
        failure: { process: "app", exit_code: 1, cause: "host_capacity" },
      }),
    );

    const response = await open("/");
    const html = await response.text();

    expect(response.status).toBe(503);
    expect(html).not.toContain("http-equiv=\"refresh\"");
    expect(html).toContain("out of capacity");
    expect(html).toContain("start=1");
    expect(html).toContain("Try again");
  });

  it("a failure with no known cause names the process and its exit code", async () => {
    app = parked(() =>
      boot("failed", {
        steps: [{ key: "app", label: "Starting app", state: "failed" }],
        failure: { process: "app", exit_code: 137, cause: null },
      }),
    );

    const html = await (await open("/")).text();

    expect(html).not.toContain("http-equiv=\"refresh\"");
    expect(html).toContain("exited 137");
    expect(html).toContain("Try again");
  });

  it("a failure page stays a failure page on reload", async () => {
    app = parked(() => boot("failed", { failure: { process: "app", exit_code: 1, cause: null } }));

    expect(await (await open("/")).text()).toContain("did not start");
    expect(await (await open("/")).text()).toContain("did not start");
  });

  // A box that would not start is a failure of its own: no run, no exit code,
  // and the sentence the boot recorded is what the person reads.
  it("a boot answering a box that did not start shows the substrate's sentence", async () => {
    app = parked(() =>
      boot("failed", {
        steps: [{ key: "box", label: "Starting your machine", state: "failed" }],
        failure: {
          process: "box",
          exit_code: null,
          cause: null,
          message: "the sandbox account has no region uk-south-1 (it has us, eu)",
        },
      }),
    );

    const html = await (await open("/")).text();

    expect(html).not.toContain('http-equiv="refresh"');
    expect(html).toContain("no region uk-south-1");
    expect(html).not.toContain("exited");
    expect(html).toContain("Try again");
  });

  it("?start=1 on a failure page asks with start=1, and shows the wait", async () => {
    app = parked((url) =>
      url.searchParams.get("start") === "1"
        ? boot("starting")
        : boot("failed", { failure: { process: "app", exit_code: 1, cause: null } }),
    );

    expect(await (await open("/")).text()).toContain("did not start");
    expect(asked("/boot")).toHaveLength(1);

    const pressed = await (await open("/?start=1")).text();
    expect(pressed).toContain("Starting this page");
    expect(pressed).not.toContain("did not start");

    const boots = fetches.filter((f) => new URL(f.url).pathname === "/boot");
    expect(boots).toHaveLength(2);
    expect(new URL(boots[1].url).searchParams.get("start")).toBe("1");
  });

  it("the wait page refreshes to a URL that still carries the token", async () => {
    // The page is served at the clean URL after the gate's 302, so a bare
    // `content="5"` refreshes the clean URL and depends on the cookie. Inside a
    // cross-site iframe a browser without partitioned cookies sends none, the
    // refresh lands 401, and the frame sits on it until the outer page is
    // reloaded. Carrying the token is what makes the refresh authorised on its
    // own; the gate then strips it again, as it does for any first load.
    app = parked(() => boot("starting"));

    const response = await open("/?x=1");
    const html = await response.text();

    expect(html).toMatch(/http-equiv="refresh" content="5; url=[^"]*token=/);
    expect(html).toContain("x=1");
  });

  it("every document asks; a page's assets never do", async () => {
    // The page a document gets is the app's answer, so each one asks. An asset
    // gets no page, and nothing it does is a press.
    app = parked(() => boot("starting"));

    await open("/");
    await open("/");
    expect(asked("/boot")).toHaveLength(2);

    const token = await sign({ host: HOST, box: "wt-1", port: 3000 });
    const asset = (path: string) =>
      worker.fetch(
        new Request(`https://${HOST}${path}`, { headers: { "x-nvoi-token": token } }),
        env as never,
        ctx as never,
      );

    await asset("/assets/app.js");
    await asset("/assets/app.css");
    expect(asked("/boot")).toHaveLength(2);
  });

  it("an asset never asks for a boot", async () => {
    // An XHR, an image or `curl` is not a person's press. It gets the bare 503
    // and starts nothing.
    app = parked(() => boot("stopped"));

    const token = await sign({ host: HOST, box: "wt-1", port: 3000 });

    await worker.fetch(
      new Request(`https://${HOST}/api/thing`, {
        headers: { "x-nvoi-token": token, accept: "application/json" },
      }),
      env as never,
      ctx as never,
    );

    expect(asked("/boot")).toHaveLength(0);
  });

  it("an asset gets a bare 503 rather than a page", async () => {
    app = parked(() => boot("stopped"));

    const token = await sign({ host: HOST, box: "wt-1", port: 3000 });

    const response = await worker.fetch(
      new Request(`https://${HOST}/app.css`, {
        headers: { "x-nvoi-token": token, accept: "text/css" },
      }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(503);
    expect(await response.text()).not.toContain("<");
  });
});

describe("the ceiling", () => {
  it("asks the app once per window about a preview that has no route", async () => {
    // A resolved route is cached and a parked one has nothing to cache, so this
    // call is the one with no bound on it: the wait page refreshes itself and
    // every subresource comes back too, and each resolve does substrate work.
    app = parked(() => boot("starting"));

    await open("/");
    await open("/");
    await open("/assets/app.js");

    expect(asked("/route")).toHaveLength(1);
  });

  it("still says the preview is coming, for every request inside that window", async () => {
    // The answer is held, not just the fact of having asked. A viewer arriving
    // second must be told what the app said, not handed a different page.
    app = parked(() => boot("starting"));

    await open("/");
    const second = await open("/");

    expect(second.status).toBe(503);
    expect(await second.text()).toContain("Starting this page");
  });

  it("passes the app's own 429 on rather than reading it as a preview that is down", async () => {
    // A ceiling reached is not a box to boot. Answering with the wait page would
    // fire a boot and refresh into the same refusal.
    app = () => new Response(null, { status: 429 });

    const response = await open("/");

    expect(response.status).toBe(429);
    expect(await response.text()).toContain("Too many requests");
    expect(asked("/boot")).toHaveLength(0);
  });

  it("gives an asset the bare 429 and a window to come back in", async () => {
    app = () => new Response(null, { status: 429 });

    const token = await sign({ host: HOST, box: "wt-1", port: 3000 });

    const response = await worker.fetch(
      new Request(`https://${HOST}/app.css`, {
        headers: { "x-nvoi-token": token, accept: "text/css" },
      }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(429);
    expect(await response.text()).not.toContain("<");
    expect(response.headers.get("retry-after")).toBe("5");
  });

  it("holds the refusal too, so a burst is not forwarded a request at a time", async () => {
    app = () => new Response(null, { status: 429 });

    await open("/");
    await open("/");
    await open("/");

    expect(asked("/route")).toHaveLength(1);
  });
});

describe("an origin that is not there", () => {
  it("reads Daytona's 400-with-a-sentence as a parked box, not a bad request", async () => {
    // Their edge reports a stopped sandbox this way, so the status alone cannot
    // tell it from a real 400.
    origin = () => new Response("Is the Sandbox started?", { status: 400 });

    const response = await open("/");

    expect(response.status).toBe(503);
    expect(await response.text()).toContain("is not running");
  });

  it("reads a 404 naming the SANDBOX as a box that is gone", async () => {
    // A deleted box answers 404 carrying the substrate's own JSON, and proxying it
    // showed a viewer their provider's error, its sandbox id and its internal
    // path — for a preview that simply needs booting.
    origin = () =>
      new Response(
        JSON.stringify({
          statusCode: 404,
          message: "not found: sandbox 850b53c8 not found, it may have been deleted or stopped",
          code: "NOT_FOUND",
          path: "/sandboxes/850b53c8/toolbox/proxy/3000/",
        }),
        { status: 404 },
      );

    const response = await open("/");

    expect(response.status).toBe(503);
    expect(await response.text()).toContain("is not running");
  });

  it("passes the APP's own 404 through", async () => {
    // A status code alone cannot tell a deleted box from a page the app does
    // not have — and swallowing the second would hide every broken link.
    origin = () => new Response("No route matches /nope", { status: 404 });

    const response = await open("/nope");

    expect(response.status).toBe(404);
    expect(await response.text()).toBe("No route matches /nope");
  });

  it("does not leak the substrate's sandbox id to a viewer", async () => {
    origin = () =>
      new Response(JSON.stringify({ message: "not found: sandbox 850b53c8 not found" }), {
        status: 404,
      });

    const body = await (await open("/")).text();

    expect(body).not.toContain("850b53c8");
    expect(body).not.toContain("NOT_FOUND");
  });

  it("passes a genuine 400 through", async () => {
    origin = () => new Response("that is not a valid form", { status: 400 });

    const response = await open("/");

    expect(response.status).toBe(400);
    expect(await response.text()).toBe("that is not a valid form");
  });

  it("forgets the cached route BEFORE retrying, because the token rotated", async () => {
    origin = () => new Response(null, { status: 502 });

    await open("/");

    expect(cache.has("https://nvoi-route.internal/" + HOST)).toBe(false);
  });
});

describe("activity", () => {
  it("stamps once per window, however many assets a page pulls", async () => {
    await open("/");
    await open("/app.css");
    await open("/app.js");

    expect(asked("/touch")).toHaveLength(1);
  });

  it("stamps only after the gate passes", async () => {
    await worker.fetch(new Request(`https://${HOST}/`), env as never, ctx as never);

    expect(asked("/touch")).toHaveLength(0);
  });
});

describe("framing", () => {
  it("removes x-frame-options and frame-ancestors, and keeps the rest of a policy", async () => {
    origin = () =>
      new Response("ok", {
        headers: {
          "x-frame-options": "DENY",
          "content-security-policy": "default-src 'self'; frame-ancestors 'none'",
        },
      });

    const response = await open("/");

    expect(response.headers.get("x-frame-options")).toBeNull();
    expect(response.headers.get("content-security-policy")).toBe("default-src 'self'");
  });
});

describe("the bridge", () => {
  it("serves its script without a token, naming the parent origins", async () => {
    const response = await worker.fetch(
      new Request(`https://${HOST}/__nvoi/bridge.js`),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(200);
    expect(response.headers.get("content-type")).toContain("text/javascript");
    expect(response.headers.get("cache-control")).toBe("no-cache");
    const body = await response.text();
    expect(body).toContain('["https://dev.nvoi.to","https://app.nvoi.to"]');
    expect(body).toContain("nvoi:ping");
    expect(fetches).toHaveLength(0);
  });

  it("adds its script first in the head of an HTML page", async () => {
    origin = () =>
      new Response('<!doctype html><html><head lang="en"><title>x</title></head><body>hi</body></html>', {
        headers: { "content-type": "text/html; charset=utf-8" },
      });

    const html = await (await open("/")).text();

    expect(html).toContain('<head lang="en"><script src="/__nvoi/bridge.js"></script><title>');
  });

  it("leaves a page without a head alone", async () => {
    origin = () => new Response("<p>bare</p>", { headers: { "content-type": "text/html" } });

    expect(await (await open("/")).text()).toBe("<p>bare</p>");
  });

  it("leaves anything that is not HTML alone", async () => {
    origin = () =>
      new Response('{"head":"<head>"}', { headers: { "content-type": "application/json" } });

    expect(await (await open("/data.json")).text()).toBe('{"head":"<head>"}');
  });
});

describe("the token does not stay in the URL", () => {
  it("moves it into a cookie and sends the browser to the clean address", async () => {
    // The secret then lives in one place a page cannot read, and never in the
    // address bar, the history or a Referer header.
    const token = await sign({ host: HOST, box: "wt-1", port: 3000 });

    const response = await worker.fetch(
      new Request(`https://${HOST}/dashboard?token=${token}&keep=this`, {
        headers: { accept: "text/html" },
      }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(302);
    expect(response.headers.get("location")).toBe("/dashboard?keep=this");
    expect(response.headers.get("set-cookie")).toContain("HttpOnly");
  });

  it("never forwards it to the origin", async () => {
    const token = await sign({ host: HOST, box: "wt-1", port: 3000 });

    await worker.fetch(
      new Request(`https://${HOST}/app.css?token=${token}`, {
        headers: { accept: "text/css" },
      }),
      env as never,
      ctx as never,
    );

    expect(reached()[0].url).not.toContain("token=");
  });
});

/** An upgrade carries no `accept: text/html`, so it is never a document. */
async function socket(path = "/socket", origin?: string): Promise<Response> {
  const token = await sign({ host: HOST, box: "wt-1", port: 3000 });

  return worker.fetch(
    new Request(`https://${HOST}${path}`, {
      headers: {
        "x-nvoi-token": token,
        upgrade: "websocket",
        connection: "Upgrade",
        "sec-websocket-version": "13",
        ...(origin ? { origin } : {}),
      },
    }),
    env as never,
    ctx as never,
  );
}

/** An origin that takes the connection and then says nothing, ever. */
const hangs = () =>
  new Promise<Response>((_resolve, reject) => {
    const { signal } = fetches[fetches.length - 1].init as { signal: AbortSignal };
    signal.addEventListener("abort", () => reject(signal.reason));
  });

/** Real turns of the loop: node's crypto does not move on the fake clock. */
async function until(ready: () => boolean): Promise<void> {
  for (let turn = 0; turn < 100 && !ready(); turn++) {
    await new Promise((resolve) => setImmediate(resolve));
  }
}

// This runtime cannot represent a 101, so what is asserted is the signal the
// worker hands `fetch`. A real preview proves the handshake.
describe("a websocket upgrade", () => {
  it("forwards `Upgrade` and the route's headers", async () => {
    // The origin answers 426 to a WebSocket whose `Upgrade` was lost, which is
    // why the request is never rebuilt.
    await socket();

    const [call] = reached();
    expect(call.url).toBe("https://3000-sb-1.proxy.daytona.works/socket");

    const headers = new Headers(call.init?.headers as HeadersInit);
    expect(headers.get("upgrade")).toBe("websocket");
    expect(headers.get("x-daytona-preview-token")).toBe("tok-1");
  });

  it("checks the page's origin itself and does not pass it on, since the vendors refuse any", async () => {
    await socket("/socket", `https://${HOST}`);

    const headers = new Headers(reached()[0].init?.headers as HeadersInit);
    expect(headers.get("origin")).toBeNull();
  });

  it("refuses a socket opened from another site's page", async () => {
    const response = await socket("/socket", "https://elsewhere.test");

    expect(response.status).toBe(403);
    expect(reached()).toHaveLength(0);
  });

  it("tells the origin the address the page is on", async () => {
    await socket();

    const headers = new Headers(reached()[0].init?.headers as HeadersInit);
    expect(headers.get("x-forwarded-host")).toBe(HOST);
    expect(headers.get("x-forwarded-proto")).toBe("https");
    expect(headers.get("x-nvoi-host")).toBe(HOST);
  });

  it("carries a bound on the connect", async () => {
    await socket();

    expect((reached()[0].init as RequestInit).signal).toBeInstanceOf(AbortSignal);
  });

  it("gives up on an origin that never completes the handshake, and starts nothing", async () => {
    // Bun fakes the timers and leaves `setImmediate` alone, so `until` still
    // gets real turns of the loop. The
    // clock may not move before the origin is reached: the bound is set there,
    // and a timer scheduled after the advance never fires.
    jest.useFakeTimers();
    origin = hangs;

    try {
      const answering = socket();
      await until(() => reached().length > 0);
      jest.advanceTimersByTime(8_000);

      const response = await answering;

      expect(response.status).toBe(503);
      expect(asked("/boot")).toHaveLength(0);
    } finally {
      jest.useRealTimers();
    }
    // Fake timers, but `until` waits on real
    // loop turns, so a loaded runner can exceed the 5s default.
  }, 20_000);

  it("stops counting once the socket is open, so a live conversation is never cut", async () => {
    jest.useFakeTimers();

    try {
      const response = await socket();
      const { signal } = reached()[0].init as { signal: AbortSignal };

      jest.advanceTimersByTime(60_000);

      expect(response.status).toBe(200);
      expect(signal.aborted).toBe(false);
    } finally {
      jest.useRealTimers();
    }
  });
});

describe("redirects belong to the browser", () => {
  it("passes a 302 from the app through rather than following it", async () => {
    origin = () => new Response(null, { status: 302, headers: { location: "/login" } });

    const response = await open("/dashboard");

    expect(response.status).toBe(302);
    expect(response.headers.get("location")).toBe("/login");
    expect((reached()[0].init as RequestInit).redirect).toBe("manual");
  });
});

// The whole route rotates, headers included — the proxy sets what it is handed,
// so a token that moved only in `token` would never reach the origin.
const rotated = (token: string) => ({
  ...ROUTE,
  token,
  headers: { ...ROUTE.headers, "x-daytona-preview-token": token },
});

describe("a token that rotated", () => {
  it("is a stale route, not the app saying no", async () => {
    // The substrate's preview token rotates when a box parks and comes back, and
    // its edge refuses the old one with a 401. Without this a woken box served
    // 401 to every request until the cache expired, and a full reload looked
    // like "it never came back".
    let issued = 0;

    app = (path) =>
      path === "/route"
        ? new Response(JSON.stringify(rotated(`tok-${++issued}`)), {
            headers: { "content-type": "application/json" },
          })
        : new Response(null, { status: 204 });

    origin = () => {
      const headers = new Headers(fetches[fetches.length - 1].init?.headers as HeadersInit);
      return headers.get("x-daytona-preview-token") === "tok-2"
        ? new Response("the app")
        : new Response("unauthorized", { status: 401 });
    };

    const response = await open("/");

    expect(response.status).toBe(200);
    expect(await response.text()).toBe("the app");
    // Asked twice: once from cache, once afresh after the refusal.
    expect(asked("/route")).toHaveLength(2);
  });

  it("retries a POST with its body, not with an empty one", async () => {
    // `passthrough` sets `body: request.body`, and a Request body is a stream —
    // the first attempt consumes it. Re-read for the retry it is already
    // disturbed, so the retry the rotation handling exists for either throws or
    // sends nothing, and a form POST to a woken box gets a wake page instead of
    // being replayed.
    let issued = 0;

    app = (path) =>
      path === "/route"
        ? new Response(JSON.stringify(rotated(`tok-${++issued}`)), {
            headers: { "content-type": "application/json" },
          })
        : new Response(null, { status: 204 });

    origin = () => {
      const headers = new Headers(fetches[fetches.length - 1].init?.headers as HeadersInit);

      return headers.get("x-daytona-preview-token") === "tok-2"
        ? new Response("saved")
        : new Response("unauthorized", { status: 401 });
    };

    const response = await open("/submit", { method: "POST", body: "name=ada" });

    expect(response.status).toBe(200);
    expect(await response.text()).toBe("saved");

    // Both attempts carried it. Read off the recorded init, because the body is
    // a stream and this is the only place holding a reference to each one.
    const sent = await Promise.all(
      reached().map((call) => new Response(call.init?.body as BodyInit).text()),
    );

    expect(sent).toEqual(["name=ada", "name=ada"]);
  });

  it("a SECOND refusal is the app's own, and passes through", async () => {
    origin = () => new Response("sign in", { status: 401 });

    const response = await open("/");

    expect(response.status).toBe(401);
    expect(await response.text()).toBe("sign in");
  });
});

describe("the viewer header", () => {
  const ARTIFACT_HOST = "ledger-x1y2z3-preview-dev.nvoi.to";
  const KEY = "viewer-key-1";
  const VIEWER = { id: 7, email: "ada@example.com", name: "Ada Lövelace" };

  beforeEach(() => {
    app = (path) =>
      path === "/route"
        ? new Response(JSON.stringify({ ...ROUTE, viewer_key: KEY }), {
            headers: { "content-type": "application/json" },
          })
        : new Response(null, { status: 204 });
  });

  async function visit(
    claims: Record<string, unknown> = { viewer: VIEWER },
    headers: Record<string, string> = {},
    path = "/",
    host = ARTIFACT_HOST,
  ): Promise<Response> {
    const token = await sign({ host, box: "art-1", port: 3000, ...claims });

    return worker.fetch(
      new Request(`https://${host}${path}`, { headers: { ...headers, "x-nvoi-token": token } }),
      env as never,
      ctx as never,
    );
  }

  const sent = (call = reached()[0]) => new Headers(call.init?.headers as HeadersInit);

  it("signs the viewer with the route's key, over the viewer and the token's expiry", async () => {
    await visit();

    const header = sent().get("x-nvoi-viewer") as string;
    const [payload, sig] = header.split(".");

    const key = await crypto.subtle.importKey(
      "raw",
      new TextEncoder().encode(KEY),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["verify"],
    );
    const valid = await crypto.subtle.verify(
      "HMAC",
      key,
      Buffer.from(sig, "base64url"),
      new TextEncoder().encode(payload),
    );
    const decoded = JSON.parse(Buffer.from(payload, "base64url").toString("utf8"));

    expect(valid).toBe(true);
    expect(decoded).toMatchObject(VIEWER);
    expect(decoded.exp).toBeGreaterThan(Date.now() / 1000);
    expect(header).toBe(await seal(VIEWER, decoded.exp, KEY));
  });

  it("sets the viewer on a websocket upgrade too", async () => {
    await visit({ viewer: VIEWER }, { upgrade: "websocket", connection: "Upgrade" }, "/cable");

    expect(sent().get("x-nvoi-viewer")).toMatch(/^[\w-]+\.[\w-]+$/);
  });

  it("replaces a viewer header the visitor sent", async () => {
    await visit({ viewer: VIEWER }, { "x-nvoi-viewer": "forged.header" });

    expect(sent().get("x-nvoi-viewer")).not.toBe("forged.header");
  });

  it("gives an anonymous visitor no viewer header, even one they sent", async () => {
    await visit({}, { "x-nvoi-viewer": "forged.header" });

    expect(sent().has("x-nvoi-viewer")).toBe(false);
  });

  it("strips a viewer header when the route carries no key", async () => {
    await open("/", { headers: { "x-nvoi-viewer": "forged.header" } });

    expect(sent().has("x-nvoi-viewer")).toBe(false);
  });
});
