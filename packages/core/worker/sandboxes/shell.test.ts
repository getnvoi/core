import { describe, expect, it } from "bun:test";

import { busy, expired, progress, refused, stopped, type Step } from "./shell.ts";

/**
 * The progress page, which is THE ONLY SCREEN a viewer ever sees for a preview
 * that is not serving. A step whose state this file does not know about renders
 * as the literal string `undefined` beside its label — which is what a person
 * saw, in production, above "Starting app".
 */
describe("progress", () => {
  const html = async (steps: Step[]) => await progress("wt-1-preview-dev.nvoi.to", steps, { refresh: 5 }).text();

  it("a state this file has not heard of draws as not-yet-known", async () => {
    // `Step["state"]` is a type over data that arrives as JSON from the server. A
    // state added to the steps `Nvoi.Preview` sends has no row in `ICON`, and indexing it
    // gave `undefined` — rendered literally, beside the label, on the page
    // somebody is watching while they wait.
    const rendered = await html([
      { key: "new", label: "Something new", state: "reticulating" as Step["state"] },
    ]);

    expect(rendered).not.toContain("undefined");
    expect(rendered).toContain("Something new");
    expect(rendered).toContain("<svg");
  });

  it("draws an icon for every state the type names", async () => {
    // `Nvoi.Preview` sends running, done and failed; `pending` and `dormant` are
    // the type's too, and each draws its own icon.
    const states: Step["state"][] = ["pending", "running", "done", "failed", "dormant"];

    for (const state of states) {
      const body = await html([{ key: "app", label: "Starting app", state }]);

      expect(body, `state ${state} renders no icon`).not.toContain("undefined");
      expect(body).toContain("<svg");
    }
  });

  it("shows dormant as not-yet-known rather than as failed", async () => {
    // Nobody telling us is not a failure. Drawing an ✕ there would report a
    // broken app to somebody whose app is fine and simply has no viewer.
    const body = await html([{ key: "app", label: "Starting app", state: "dormant" }]);

    expect(body).toContain("Starting app");
    expect(body).not.toContain('d="M18 6 6 18"');
  });

  it("escapes a label rather than trusting it", async () => {
    const body = await html([
      { key: "app", label: "<img src=x onerror=alert(1)>", state: "done" },
    ]);

    expect(body).not.toContain("<img");
  });
});

/**
 * What every page says is plain text a person reads: no em dash, which the
 * house style keeps out of copy, and no word about how the page is hosted.
 */
describe("the words on every page", () => {
  const host = "board-abc123-preview-dev.nvoi.to";

  it("carry no em dash and never name a container or a checkout", async () => {
    const pages = [
      progress(host, [], { refresh: 5 }),
      refused(host),
      expired(host),
      busy(host),
      stopped(host, "/?start=1"),
    ];
    for (const page of pages) {
      const text = await page.text();
      expect(text).not.toContain("\u2014");
      expect(text).not.toMatch(/container|fresh checkout/i);
    }
  });
});
