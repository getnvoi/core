import { afterEach, describe, expect, it, vi } from "vitest";

import { DEV_VIEWER, HEADER, requireViewer, sign, verify, viewer } from "~/services/viewer.server";

const person = { id: "42", email: "ada@example.com", name: "Ada" };
const later = () => Math.floor(Date.now() / 1000) + 60;

function request(header?: string) {
  return new Request("http://localhost/", { headers: header ? { [HEADER]: header } : {} });
}

// The vector the core's test/nvoi/gate_contract_test.exs computes and the preview worker's seal must match.
const CONTRACT = {
  viewer: { id: "u-1", email: "ada@nvoi.to", name: "Ada Lövelace" },
  exp: 4102444800,
  key: "0f".repeat(32),
  header:
    "eyJpZCI6InUtMSIsImVtYWlsIjoiYWRhQG52b2kudG8iLCJuYW1lIjoiQWRhIEzDtnZlbGFjZSIsImV4cCI6NDEwMjQ0NDgwMH0" +
    ".yzg64Ka_pQEdWMYsSlSozBrjO7tf53xrafvOGnSMbUo",
};

describe("viewer", () => {
  afterEach(() => vi.unstubAllEnvs());

  it("reads the header the worker seals, as the gate contract computes it", () => {
    expect(verify(CONTRACT.header, CONTRACT.key)).toEqual(CONTRACT.viewer);
    expect(sign(CONTRACT.viewer, CONTRACT.exp, CONTRACT.key)).toBe(CONTRACT.header);
  });

  it("reads a signed header", () => {
    expect(viewer(request(sign(person, later(), "test-key")))).toEqual(person);
  });

  it("reads the headers of a Node upgrade request", () => {
    expect(viewer({ headers: { [HEADER]: sign(person, later(), "test-key") } })).toEqual(person);
  });

  it("accepts a numeric id as a string", () => {
    const header = sign({ ...person, id: 7 as unknown as string }, later(), "test-key");
    expect(verify(header, "test-key")?.id).toBe("7");
  });

  it("is anonymous without a header", () => {
    expect(viewer(request())).toBeNull();
  });

  it("rejects another key, a changed payload and an expired header", () => {
    expect(viewer(request(sign(person, later(), "other-key")))).toBeNull();

    const [, sig] = sign(person, later(), "test-key").split(".");
    const forged = Buffer.from(JSON.stringify({ ...person, id: "1", exp: later() })).toString("base64url");
    expect(viewer(request(`${forged}.${sig}`))).toBeNull();

    expect(viewer(request(sign(person, Math.floor(Date.now() / 1000) - 1, "test-key")))).toBeNull();
    expect(viewer(request("garbage"))).toBeNull();
  });

  it("uses the dev viewer only outside production when no key is set", () => {
    vi.stubEnv("NVOI_VIEWER_KEY", "");
    vi.stubEnv("NODE_ENV", "development");
    expect(viewer(request())).toEqual(DEV_VIEWER);

    vi.stubEnv("NODE_ENV", "production");
    expect(viewer(request())).toBeNull();
  });

  it("answers 401 when a viewer is required", () => {
    try {
      requireViewer(request());
      expect.unreachable();
    } catch (error) {
      expect((error as Response).status).toBe(401);
    }
  });
});
