import { readFileSync } from "node:fs";

import { afterAll, beforeAll, describe, expect, it, setSystemTime } from "bun:test";

import { gate, seal, terminalGate } from "./gate.ts";

/**
 * The other half of the server's `test/nvoi/gate_contract_test.exs`.
 *
 * The tokens in `index.test.ts` are minted here, in TypeScript, from claims this
 * file also writes — so they agree with whoever wrote them and say nothing about
 * what the server signs. These come out of the server's suite, through the call
 * a real link goes through (`Nvoi.Gate`).
 *
 * The verdicts are written HERE and not carried in the fixture. One that shipped
 * its own expectations would agree with whatever the signer had just been changed
 * to; stated separately, a claim renamed in `Nvoi.Gate` arrives as a token
 * missing it and the row saying "serve this" fails.
 */
const FIXTURE = new URL("../../tmp/gate-contract.json", import.meta.url);

interface Row {
  id: string;
  token: string;
}

interface Fixture {
  /** The second the tokens were minted. The clock is frozen here. */
  now: number;
  secret: string;
  host: string;
  /** The host the artifact rows are bound to. */
  artifact_host: string;
  /** A viewer, a key and the header the server computed from them, as the spec describes it. */
  seal: { viewer: { id: string; email: string; name: string }; exp: number; key: string; header: string };
  rows: Row[];
}

let fixture: Fixture;

try {
  fixture = JSON.parse(readFileSync(FIXTURE, "utf8")) as Fixture;
} catch {
  throw new Error(
    "the gate contract fixture is not there. The server's suite writes it: mix test test/nvoi/gate_contract_test.exs, in packages/core",
  );
}

/** "serves" is the gate answering nothing at all, which is a request let through. */
const EDGE: Record<string, "serves" | number> = {
  preview: "serves",
  // Not a 401. A token that verifies and names no port is a preview whose port
  // was never reported, and guessing one proxies at whatever is listening.
  no_port: 503,
  wrong_audience: 401,
  no_aud: 401,
  other_host: 401,
  expired: 401,
  no_exp: 401,
  bad_signature: 401,
  tampered_payload: 401,
  alg_none: 401,
  // A terminal token opens no preview.
  terminal: 401,
  terminal_other_host: 401,
  terminal_no_box: 401,
  terminal_expired: 401,
  // An artifact's link is bound to the artifact's own host.
  artifact: 401,
  artifact_anonymous: 401,
  artifact_expired: 401,
};

/** The same rows, judged on an artifact's host. Only a live link for that host opens it. */
const APP: Record<string, "serves" | number> = {
  ...Object.fromEntries(Object.keys(EDGE).map((id) => [id, 401])),
  artifact: "serves",
  artifact_anonymous: "serves",
};

/** The same rows, judged at the terminal path. Only a terminal token for this host opens a shell. */
const SHELL: Record<string, "serves" | number> = {
  ...Object.fromEntries(Object.keys(EDGE).map((id) => [id, 401])),
  terminal: "serves",
};

const PARENTS = ["https://dev.nvoi.to"];

describe("the tokens the server signs, judged at the preview edge", () => {
  // Frozen at the moment they were minted, so `expired` is stale and the rest
  // are fresh however long the file has been sitting there.
  beforeAll(() => setSystemTime(new Date(fixture.now * 1000)));

  afterAll(() => setSystemTime());

  it("judges every row that was written, and no row that was not", () => {
    expect(fixture.rows.map((row) => row.id).sort()).toEqual(Object.keys(EDGE).sort());
  });

  for (const row of fixture.rows) {
    it(row.id, async () => {
      const result = await gate(request(row.token), fixture.secret);
      const expected = EDGE[row.id];

      if (expected === "serves") {
        expect(result.response).toBeUndefined();
      } else {
        expect(result.response?.status).toBe(expected);
      }
    });
  }

  for (const row of fixture.rows) {
    it(`${row.id}, at the terminal path`, async () => {
      const result = await terminalGate(request(row.token, "/__nvoi/pty/sh-1"), fixture.secret, PARENTS);
      const expected = SHELL[row.id];

      if (expected === "serves") {
        expect(result.response).toBeUndefined();
      } else {
        expect(result.response?.status).toBe(expected);
      }
    });
  }

  for (const row of fixture.rows) {
    it(`${row.id}, on the artifact host`, async () => {
      const presented = new Request(`https://${fixture.artifact_host}/`, { headers: { "x-nvoi-token": row.token } });
      const result = await gate(presented, fixture.secret);
      const expected = APP[row.id];

      if (expected === "serves") {
        expect(result.response).toBeUndefined();
      } else {
        expect(result.response?.status).toBe(expected);
      }
    });
  }

  it("the viewer a signed-in artifact token carries is sealed as the server computes it", async () => {
    const { claims } = await gate(
      new Request(`https://${fixture.artifact_host}/`, { headers: { "x-nvoi-token": token("artifact") } }),
      fixture.secret,
    );

    expect(Object.keys(claims.viewer ?? {}).sort()).toEqual(["email", "id", "name"]);
    expect(await seal(fixture.seal.viewer, fixture.seal.exp, fixture.seal.key)).toBe(fixture.seal.header);
  });

  it("the terminal it serves names a box on this host", async () => {
    const { claims } = await terminalGate(request(token("terminal"), "/__nvoi/pty/sh-1"), fixture.secret, PARENTS);

    expect(claims.host).toBe(fixture.host);
    expect(typeof claims.box).toBe("string");
  });

  it("the preview it serves names a box and an integer port", async () => {
    // The claims interface is optional in all three, so TypeScript agrees with a
    // token carrying none of them. This is where that is checked.
    const { claims } = await gate(request(token("preview")), fixture.secret);

    expect(claims.host).toBe(fixture.host);
    expect(typeof claims.box).toBe("string");
    expect(Number.isInteger(claims.port)).toBe(true);
  });
});

function request(presented: string, path = "/"): Request {
  return new Request(`https://${fixture.host}${path}`, { headers: { "x-nvoi-token": presented } });
}

function token(id: string): string {
  const row = fixture.rows.find((candidate) => candidate.id === id);
  if (!row) throw new Error(`the fixture has no ${id} row`);

  return row.token;
}
