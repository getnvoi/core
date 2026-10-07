import { existsSync } from "node:fs";
import type { AddressInfo } from "node:net";

import { createRequestHandler } from "@react-router/express";
import express from "express";
import { afterAll, beforeAll, describe, expect, it } from "vitest";

import { edge } from "../../server/edge";

// A form posted from the page, as nvoi's edge delivers it: Host is the machine's address, with a port
// (the test server's own, as a server's :8080 would be),
// X-Forwarded-Host whatever the vendor's edge rewrote it to, x-nvoi-host and Origin the page's own. Runs
// the built app (npm run build first), through Express, as the server does.
const PAGE = "team-todo-abc123-preview.nvoi.to";
const BUILD = new URL("../../build/server/index.js", import.meta.url);

let base = "";
let close = () => {};

beforeAll(async () => {
  if (!existsSync(BUILD)) throw new Error("run `npm run build` before the edge test");
  const app = express();
  edge(app);
  app.all("*", createRequestHandler({ build: () => import(BUILD.href) }));
  const server = app.listen(0);
  await new Promise((resolve) => server.once("listening", resolve));
  base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
  close = () => server.close();
});

afterAll(() => close());

function post(origin: string) {
  return fetch(`${base}/?index`, {
    method: "POST",
    headers: {
      origin,
      host: "box-node-1-4100.on.boat.dev",
      "x-forwarded-host": "box-node-1-4100.on.boat.dev",
      "x-forwarded-proto": "https",
      "x-nvoi-host": PAGE,
      "content-type": "application/x-www-form-urlencoded",
    },
    body: "intent=check",
    redirect: "manual",
  });
}

describe("a form behind nvoi's edge", () => {
  it("reaches the action when it comes from the page", async () => {
    const response = await post(`https://${PAGE}`);
    expect(await response.text()).not.toContain("Bad Request");
    expect(response.status).not.toBe(400);
  });

  it("is refused when it comes from another site", async () => {
    const response = await post("https://elsewhere.example");
    expect(response.status).toBe(400);
  });
});
