import { beforeEach, describe, expect, it } from "bun:test";

/**
 * The terminal path, `/__nvoi/pty/<id>`: what reaches the shell server and what
 * is refused. The setup is `index.test.ts`'s in small: the app and the origin are
 * functions over the request, the cache a map.
 */
const { default: worker } = await import("./index.ts");

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

// What the app answers a terminal token: the shell server's port, and the
// machine's key beside the substrate's own headers.
const SHELLS = {
  host: "7777-sb-1.proxy.daytona.works",
  token: "tok-1",
  headers: { "x-daytona-preview-token": "tok-1", "X-Nvoi-Terminal": "machine-key" },
};

// And a preview token: the app's port, no key.
const PREVIEW = {
  host: "3000-sb-1.proxy.daytona.works",
  token: "tok-1",
  headers: { "x-daytona-preview-token": "tok-1" },
};

const cache = new Map<string, Response>();
let fetches: Array<{ url: string; init?: RequestInit }> = [];
let origin: () => Response;
let app: (path: string, audience: string) => Response;

const json = (body: unknown) => new Response(JSON.stringify(body), { headers: { "content-type": "application/json" } });

/** The audience of the token a call to the app carried. */
function audience(init?: RequestInit): string {
  const token = new Headers(init?.headers as HeadersInit).get("x-nvoi-token") ?? "";
  const payload = token.split(".")[1] ?? "";
  return JSON.parse(atob(payload.replace(/-/g, "+").replace(/_/g, "/"))).aud;
}

beforeEach(() => {
  cache.clear();
  fetches = [];
  origin = () => new Response("switching", { status: 200 });
  app = (path, aud) =>
    path === "/route" ? json(aud === "terminal" ? SHELLS : PREVIEW) : new Response(null, { status: 204 });

  (globalThis as { caches: unknown }).caches = {
    default: {
      match: async (request: Request) => cache.get(request.url)?.clone(),
      put: async (request: Request, response: Response) => void cache.set(request.url, response),
      delete: async (request: Request) => cache.delete(request.url),
    },
  };

  globalThis.fetch = (async (input: string | Request, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input.url;
    fetches.push({ url, init });
    if (url.startsWith(APP)) return app(new URL(url).pathname, audience(init));
    return origin();
  }) as typeof fetch;
});

// Assembled the way `Gate` assembles a token.
async function sign(claims: Record<string, unknown>, expiresIn = 300): Promise<string> {
  const encode = (value: unknown) =>
    btoa(JSON.stringify(value)).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
  const input = `${encode({ alg: "HS256", typ: "JWT" })}.${encode({
    aud: "terminal",
    exp: Math.floor(Date.now() / 1000) + expiresIn,
    ...claims,
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

type Options = {
  claims?: Record<string, unknown>;
  headers?: Record<string, string>;
  where?: "query" | "header" | "cookie";
  upgrade?: boolean;
};

async function shell(path = "/__nvoi/pty/sh-1?cols=80&rows=24", options: Options = {}): Promise<Response> {
  const { claims = {}, headers = {}, where = "query", upgrade = true } = options;
  const token = await sign({ host: HOST, box: "wt-1", ...claims });
  const url = new URL(`https://${HOST}${path}`);
  if (where === "query") url.searchParams.set("token", token);
  const carried: Record<string, string> =
    where === "header" ? { "x-nvoi-token": token } : where === "cookie" ? { cookie: `nvoi_token=${token}` } : {};
  const socket: Record<string, string> = upgrade
    ? { upgrade: "websocket", connection: "Upgrade", "sec-websocket-version": "13" }
    : {};

  return worker.fetch(
    new Request(url, { headers: { ...socket, origin: "https://dev.nvoi.to", ...carried, ...headers } }),
    env as never,
    ctx as never,
  );
}

const routes = () => fetches.filter((call) => call.url === `${APP}/route`);
const boots = () => fetches.filter((call) => call.url.startsWith(`${APP}/boot`));
const reached = (port: number) => fetches.filter((call) => call.url.includes(`${port}-sb-1`));
const sent = (call: { init?: RequestInit }) => new Headers(call.init?.headers as HeadersInit);

describe("a terminal socket", () => {
  it("reaches /pty/<id> on the shell server with the machine's key, the size kept, the token and cookies dropped", async () => {
    await shell(undefined, { headers: { cookie: "nvoi_token=the-preview-cookie" } });

    const [call] = reached(7777);
    expect(call.url).toBe("https://7777-sb-1.proxy.daytona.works/pty/sh-1?cols=80&rows=24");
    const headers = sent(call);
    expect(headers.get("upgrade")).toBe("websocket");
    expect(headers.get("x-nvoi-terminal")).toBe("machine-key");
    expect(headers.get("x-daytona-preview-token")).toBe("tok-1");
    // The visitor's preview cookie is a credential the shell server has no use for.
    expect(headers.get("cookie")).toBeNull();
    expect(routes().map((call) => audience(call.init))).toEqual(["terminal"]);
  });

  it("takes the token from a header too, and does not pass it on", async () => {
    await shell(undefined, { where: "header" });

    const [call] = reached(7777);
    expect(sent(call).get("x-nvoi-token")).toBeNull();
  });

  it("never from a cookie, and never sets one", async () => {
    const response = await shell(undefined, { where: "cookie" });

    expect(response.status).toBe(401);
    expect(response.headers.get("set-cookie")).toBeNull();
    expect(fetches).toHaveLength(0);
  });

  it("refuses a preview token, with nothing asked", async () => {
    const response = await shell(undefined, { claims: { aud: "preview", port: 7777 } });

    expect(response.status).toBe(401);
    expect(fetches).toHaveLength(0);
  });

  it("refuses a token minted for another hostname, and one past its expiry", async () => {
    expect((await shell(undefined, { claims: { host: "wt-2-preview-dev.nvoi.to" } })).status).toBe(401);
    expect((await shell(undefined, { claims: { exp: 1 } })).status).toBe(401);
    expect(fetches).toHaveLength(0);
  });

  it("refuses a page on another origin before looking at the token", async () => {
    const response = await shell(undefined, { headers: { origin: "https://elsewhere.example" } });

    expect(response.status).toBe(403);
    expect(fetches).toHaveLength(0);
  });

  it("refuses a bad shell id, and anything that is not a socket", async () => {
    expect((await shell("/__nvoi/pty/a.b")).status).toBe(400);
    expect((await shell(undefined, { upgrade: false })).status).toBe(426);
    expect(reached(7777)).toHaveLength(0);
  });

  it("is refused on every preview path, where only a preview token opens anything", async () => {
    const token = await sign({ host: HOST, box: "wt-1" });
    const response = await worker.fetch(
      new Request(`https://${HOST}/?token=${token}`, { headers: { accept: "text/html" } }),
      env as never,
      ctx as never,
    );

    expect(response.status).toBe(401);
    expect(response.headers.get("set-cookie")).toBeNull();
    expect(routes()).toHaveLength(0);
  });

  it("keeps its route apart from the preview's, so a preview request never carries the key", async () => {
    await shell();
    const preview = await sign({ aud: "preview", host: HOST, box: "wt-1", port: 3000 });
    await worker.fetch(
      new Request(`https://${HOST}/`, { headers: { "x-nvoi-token": preview } }),
      env as never,
      ctx as never,
    );

    expect(routes().map((call) => audience(call.init))).toEqual(["terminal", "preview"]);
    const [call] = reached(3000);
    expect(sent(call).get("x-nvoi-terminal")).toBeNull();
  });

  it("a machine with no route is starting up, and is never booted from here", async () => {
    app = (path) => new Response(null, { status: path === "/route" ? 503 : 204 });

    const response = await shell();

    expect(response.status).toBe(503);
    expect(boots()).toHaveLength(0);
    expect(reached(7777)).toHaveLength(0);
  });

  it("a refusal from their edge is a token that rotated: resolved afresh, once", async () => {
    let tries = 0;
    origin = () => new Response(null, { status: tries++ === 0 ? 401 : 200 });

    const response = await shell();

    expect(response.status).toBe(200);
    expect(routes()).toHaveLength(2);
  });
});
