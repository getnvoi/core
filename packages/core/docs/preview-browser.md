# The agent in the preview

A session's agent acts in the session's preview: the page its checkout serves, shown in the app's own browser view
(the desktop's native webview, the iPhone's WKWebView). The person sees the page the agent acts on. There is no
browser on the machine for this, no stream and no tab session.

## Tools

nvoi's tools of every session (`Nvoi.Tools.browser/0`, `lib/nvoi/tools/browser/`), served over the runner's MCP like
`set_title`, at the agent's levels (auto by default; approval asks a person first; deny leaves the tool out):

| Tool | Input | Does |
|---|---|---|
| look | | the address, title, scroll, numbered visible elements (refs) and text |
| find | query | elements of the whole page whose text, label or name holds the words, with refs |
| click, hover | ref | |
| type | ref, text, submit | replaces the field's text; submit presses Enter |
| fill | fields: [{ref, value}] | text fields, selects (value or label), checkboxes and radios at once |
| press | key | Enter, Escape, Tab, Backspace, arrows, a character |
| scroll | direction or ref | by most of a screen, or to an element |
| open | url | any http or https address, not only the preview's |
| back, forward, reload | | |
| wait | ms | at most 5000 |
| screenshot | | the view as a PNG, handed to the agent as an image and kept as a capture of the turn |
| evaluate | expression | any JavaScript expression in the page, its value as text |

Each answer but a screenshot's is the result and the page's state after it.

## A call waits like an approval

- The turn's process holds the call (`Nvoi.Runner.browse/5`): the turn is `awaiting_approval` and the agent stays
  blocked on it. Nothing else in the session starts meanwhile: a message queues.
- The server signals the session (`Nvoi.Live.signal(workspace, "sessions", id)`). The apps hear it on the live stream
  (`/socket`, the session's channel) and read the waiting calls then.
- Any app of a member who reaches the session (Access) and shows its preview performs the call. The server only
  guarantees it is performed once: the first pickup wins (`POST .../start`, an atomic update), every other app is
  refused (409 `already_picked`) and only watches. Nobody owns a preview. The person and the agent act in the same
  page freely; nothing pauses the agent while the person uses it.
- The app answers (`POST sessions/:id/preview/calls/:call_id`, `text`, `error`, a screenshot's `image` and `url`).
  The answer is the call's outcome; it tells the turn's process, which hands it to the agent, and the turn goes on
  with no extra turn.
- Deadlines, timed by the turn's process (`Nvoi.Browser.expire/2`): a call nobody picks up within 60 s fails with "nobody has this session's
  preview open"; one picked up and not answered within 60 s fails with "the app that picked it up did not answer".
  The agent reads the failure and goes on.
- A cancelled turn fails its waiting calls; a machine that stops under one fails it ("the machine stopped before the
  preview performed it").

## API

| | |
|---|---|
| `GET sessions/:id/preview/calls` | the calls waiting for the preview, oldest first; `picked` when an app took one |
| `POST sessions/:id/preview/calls/:call_id/start` | pick a call up to perform it; 409 when another app did |
| `POST sessions/:id/preview/calls/:call_id` | answer it; 409 when already answered or failed |

## Clients

- Desktop: `Preview` (`packages/clients/desktop/src/pages/browser/browser.tsx`) reads the waiting calls while it shows
  (`calls.ts`), picks each up, performs it in its site with `executor.ts` (`site_eval`, `site_shot` in `site.rs`)
  and answers.
- iPhone: the preview sheet's WKWebView does the same with the same script.

## What the webview cannot do

- Clicks and keys are events dispatched in the page (`isTrusted` false), not the system's pointer or keyboard: a
  page that checks for trusted events, file pickers, native `<select>` popups and the browser's own dialogs
  (`alert`, basic auth) do not respond to them.
- `evaluate` reads a value, it does not await a promise; the console and network logs are not kept.
- A screenshot is the visible view, not the whole page.
