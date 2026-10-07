import { EventEmitter } from "node:events";

import { describe, expect, it } from "vitest";
import type { WebSocket } from "ws";

import { broadcast, listen, relay } from "~/services/broadcast.server";

function socket() {
  const fake = Object.assign(new EventEmitter(), { OPEN: 1, readyState: 1, sent: [] as unknown[] });
  return Object.assign(fake, { send: (data: string) => fake.sent.push(JSON.parse(data)) });
}

describe("broadcast", () => {
  it("sends a viewer's messages to that viewer and public ones to everyone", async () => {
    const ada = socket();
    const visitor = socket();
    listen("1", ada as unknown as WebSocket);
    listen(null, visitor as unknown as WebSocket);

    const timer = await relay();
    await broadcast("1", { to: "ada" });
    await broadcast("2", { to: "someone else" });
    await broadcast(null, { to: "all" });
    await new Promise((resume) => setTimeout(resume, 600));
    clearInterval(timer);

    expect(ada.sent).toEqual([{ to: "ada" }, { to: "all" }]);
    expect(visitor.sent).toEqual([{ to: "all" }]);
  });
});
