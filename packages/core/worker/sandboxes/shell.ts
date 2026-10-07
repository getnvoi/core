/**
 * EVERY PAGE THE EDGE ITSELF SERVES.
 *
 * A preview reaches a person through this worker before it reaches the app, and
 * some of the answers are ours rather than theirs: the app is stopped, it is
 * coming up, a process died, the link is not valid or has expired, or it is
 * being asked for faster than we will answer. Those are the pages somebody sees
 * MOST — a box sleeps every night, so the stopped page is the first thing every
 * morning.
 *
 * ## Why it is its own module
 *
 * `gate.ts` refuses a request and `index.ts` reports on one, and both have to
 * render. Putting `page()` in either one makes the other import it and the two
 * import each other — so it lives beside them, importing nothing.
 *
 * ## Self-contained, because a Worker response has no assets
 *
 * No stylesheet, no font, no icon sprite. Anything fetched is one more thing
 * that can fail to arrive on the single page somebody looks at when things are
 * already going wrong.
 */

export type Step = {
  key: string;
  label: string;
  state: "pending" | "running" | "done" | "failed" | "dormant";
};

/**
 * THE SAME ICONS THE SESSION USES — `Check`, `Loader2`, `X`, `Circle`.
 *
 * Copied from Lucide rather than approximated with `✓ ◐ ✕ ○`: a glyph renders at
 * whatever weight and baseline the viewer's font decides, so the edge would
 * look like a different product from the app it fronts.
 */
const svg = (paths: string) =>
  `<svg class="i" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" ` +
  `stroke-linecap="round" stroke-linejoin="round">${paths}</svg>`;

const UNKNOWN = svg('<circle cx="12" cy="12" r="10"/>');

const ICON: Record<Step["state"], string> = {
  done: svg('<path d="M20 6 9 17l-5-5"/>'),
  running: svg('<path class="spin" d="M21 12a9 9 0 1 1-6.219-8.56"/>'),
  failed: svg('<path d="M18 6 6 18"/><path d="m6 6 12 12"/>'),
  pending: svg('<circle cx="12" cy="12" r="10"/>'),

  // Nobody is attached, which is not the same as down and not the same as
  // pending — `Preview.boot` answers `dormant` whenever the box is
  // telling us nothing, the commonest reading of a worktree just opened.
  //
  // It draws as not-yet-known rather than as a failure: an ✕ here reports a
  // broken app to somebody whose app is fine and merely has no viewer.
  dormant: UNKNOWN,
};

/**
 * The icon for a state, and NOT-YET-KNOWN for one this file has not heard of.
 *
 * `Step["state"]` is a type over data that arrives as JSON from another language:
 * a state added to `Preview.boot` reaches here as a string the map has no
 * row for, and `ICON[state]` is then `undefined` — rendered literally, beside
 * the label, on the page somebody is watching while they wait.
 *
 * Not-yet-known rather than failed, for the same reason `dormant` draws that
 * way: a state we do not recognise is one we cannot call broken.
 */
function icon(state: Step["state"]): string {
  return ICON[state] ?? UNKNOWN;
}

/**
 * The steps, ticking, instead of a paragraph and a blank rectangle.
 *
 * The plan comes from the server and is COMPLETE from the first request — a
 * manifest declares its setup and its services, so somebody sees "2 of 6" rather
 * than sentences arriving one at a time.
 *
 * It refreshes rather than polling with script: this page exists because a
 * request was not held, and a poller would put the waiting back where it was.
 */
// One renderer for the list, so a running provision and a stopped one cannot
// draw the same steps differently.
function list(steps: Step[]): string {
  return steps
    .map((step) => {
      // Dimmed for both, because "not started" and "not being told about"
      // are the same thing to a person watching: nothing to see here yet.
      const waiting = step.state === "pending" || step.state === "dormant";
      const dim = waiting ? ' class="pending"' : "";

      return `<li${dim}>${icon(step.state)} ${escapeHtml(step.label)}</li>`;
    })
    .join("");
}

/**
 * THE SCREEN A VIEWER SEES for a preview that is coming up.
 *
 * Bringing-up, not-listening, waking — they are one thing, a run in some
 * state, and a page per state throws away the step list in favour of prose,
 * which is exactly the view a person watching a provision needs most.
 *
 * So there is one. The steps carry the state, `ICON` carries the difference,
 * and the heading says which state the run is in. "We do not know the steps" is
 * a list with one step in it, not a different page.
 *
 * The refusal is not a provisioning state at all: an invalid token means the
 * person is not looking at a preview, and drawing a step list there would
 * confirm the worktree exists.
 */
export function progress(
  host: string,
  steps: Step[],
  // `refresh` is REQUIRED and may be `null`. It defaulted to 3, so a caller that
  // said nothing got a self-refreshing page — which is how the ERROR screen
  // came to hammer this worker every ten seconds, three lines under a comment
  // saying a self-refreshing page must never answer a defect.
  //
  // A page either waits for something that is coming or it does not, and the
  // page is the only thing that knows which. There is no sensible default.
  options: { title?: string; detail?: string; refresh: number | null; refreshTo?: string },
): Response {
  const done = steps.filter((s) => s.state === "done").length;
  const current = steps.find((s) => s.state === "running");

  // ── Steps unknown is a state of the list, not another page ────────────────
  //
  // When the server cannot be asked how far a box has got there are no steps to
  // draw, and the answer is still this screen: one step, running, saying what
  // is happening. A person watching a list keeps watching a list.
  const shown: Step[] =
    steps.length > 0 ? steps : [{ key: "waking", label: "Waking this page", state: "running" }];

  return page(
    503,
    options.title ?? (current ? current.label : "Bringing this page up…"),
    options.detail ??
      `${host} stops when nothing uses it, and is coming back up. ` +
        (steps.length > 0 ? `Step ${done + 1} of ${steps.length}. ` : "") +
        "This page refreshes itself.",
    {
      refresh: options.refresh,
      refreshTo: options.refreshTo,
      extra: `<ol class="steps">${list(shown)}</ol>`,
    },
  );
}

/**
 * The link is not valid, and that is the whole of what a viewer is told.
 *
 * NO STEP LIST HERE, deliberately. An invalid token means the person is not
 * looking at a preview, and drawing the plan would confirm the worktree exists
 * to somebody who has not been let in.
 */
export function refused(host: string): Response {
  return page(
    401,
    "This link is not valid",
    `Nothing here will open ${host}. A link is signed and expires: ` +
      "open it again from nvoi to get a current one.",
    // Nothing is coming: a token does not become valid by waiting.
    { refresh: null },
  );
}

/**
 * Our own link, past its expiry. A preview token lives an hour, so most
 * readers of a refusal are returning to yesterday's tab, and the thing they
 * can act on is opening the session again.
 */
export function expired(host: string): Response {
  return page(
    401,
    "This link has expired",
    `The link for ${host} was valid for a limited time. Open the session again ` +
      "to get a current one.",
    { refresh: null },
  );
}

/**
 * The app is refusing to answer about this preview again yet.
 *
 * No step list, because nothing is being brought up, and no refresh, because a
 * page that comes back on a timer is what the ceiling is refusing.
 */
export function busy(host: string): Response {
  return page(
    429,
    "Too many requests for this page",
    `${host} is being asked for faster than it can be answered. Nothing is ` +
      "wrong with the app: wait a moment and reload.",
    { refresh: null },
  );
}

/**
 * The preview is not coming on its own. No step list — nothing is being
 * brought up — and no refresh. The one thing on the page is a press.
 *
 * `action.href` is a path of ours, carrying our own token and `start=1`;
 * attribute-escaped like `refreshTo`, because it also carries the app's query.
 */
export function halted(options: {
  title: string;
  detail: string;
  action: { href: string; label: string };
}): Response {
  return page(503, options.title, options.detail, {
    refresh: null,
    extra: `<p class="action"><a href="${escapeHtml(options.action.href)}">${escapeHtml(options.action.label)}</a></p>`,
  });
}

/** Nobody has asked for this preview yet. A page load is not that ask; the press is. */
export function stopped(host: string, href: string): Response {
  return halted({
    title: "This page is not running",
    detail: `${host} is stopped. Starting it brings up the machine, the checkout and the app.`,
    action: { href, label: "Start" },
  });
}

/**
 * `extra` is interpolated RAW, and is the one hole in a function that otherwise
 * escapes everything. Its only caller passes a step list whose labels are
 * already through `escapeHtml/1` — so this stays private, and anything that
 * wants to pass markup here has to come and read this first.
 */
function page(
  status: number,
  title: string,
  body: string,
  opts: { refresh: number | null; refreshTo?: string; extra?: string },
): Response {
  // `refreshTo` is a path of ours carrying our own token; attribute-escaped
  // because it also carries whatever query the app's URL had.
  const refresh = opts.refresh
    ? `<meta http-equiv="refresh" content="${opts.refresh}${opts.refreshTo ? `; url=${escapeHtml(opts.refreshTo)}` : ""}">`
    : "";
  const html = `<!DOCTYPE html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
${refresh}
<title>${escapeHtml(title)}</title>
<style>
  :root { color-scheme: light dark }
  body { font: 16px/1.6 -apple-system, system-ui, sans-serif; margin: 0;
         min-height: 100vh; display: grid; place-items: center; padding: 1.5rem }
  main { max-width: 32rem; text-align: center }
  h1 { font-size: 1.5rem; font-weight: 600; letter-spacing: -.01em; margin: 0 0 .75rem }
  p { margin: 0; opacity: .7 }
  .steps { list-style: none; margin: 1.5rem 0 0; padding: 0; text-align: left;
           display: inline-block; font-size: .875rem }
  .steps li { padding: .2rem 0; display: flex; align-items: center; gap: .5rem }
  .steps li.pending { opacity: .45 }
  .action { margin-top: 1.5rem; opacity: 1 }
  .action a { display: inline-block; padding: .5rem 1rem; border-radius: .5rem;
              border: 1px solid currentColor; text-decoration: none; color: inherit }
  .i { width: 1rem; height: 1rem; flex: none }
  /* The spinner turns around the icon's own centre, not the list item's. */
  .spin { transform-origin: 12px 12px; animation: spin 1s linear infinite }
  @keyframes spin { to { transform: rotate(360deg) } }
</style>
<main>
  <h1>${escapeHtml(title)}</h1>
  <p>${escapeHtml(body)}</p>
  ${opts.extra ?? ""}
</main>
`;

  return new Response(html, {
    status,
    headers: {
      "content-type": "text/html; charset=utf-8",
      ...(opts.refresh ? { "retry-after": String(opts.refresh * 2) } : {}),
    },
  });
}

// Enough for the three places it is used, all of them element text: no
// interpolation here reaches an attribute, so `'` needs no entity.
function escapeHtml(s: string): string {
  return s.replace(
    /[&<>"]/g,
    (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!,
  );
}
