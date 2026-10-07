# Multiplayer rules

The rules the server keeps (Oct 1, 2026). Plans and reviews reference their IDs.

## Scope

nvoi is multiplayer when a workspace's members work with the same agents in shared sessions, and what one member
teaches an agent reaches every other member. The rules below adapt the Multiplayer AI manifesto (September 2026) to
nvoi's model: workspace, member, agent, agent session, turn, box, tool, gate, skill, connector, artifact.

Each rule uses MUST or MUST NOT and can be checked against the code.

## Principles

### P1 Never copy-and-paste

- P1.1 Every member who works on a task MUST talk to the same session, not their own copy of it.
- P1.2 A session MUST be reachable from every surface the work happens on: desktop, web, GitHub issue and PR comments,
  Slack. A message from any surface lands in the same transcript.
- P1.3 An agent MUST have a tool or connector for every system members use for that task. A member pasting content
  into a prompt is a missing connector.

### P2 Work with the door open

- P2.1 A session MUST be visible to every member with view access to its repository by default. Private sessions are
  the exception and are marked.
- P2.2 Members MUST see who is in a session (presence) and who wrote each turn.
- P2.3 Sessions MUST be searchable across the workspace by title, repository and transcript.

### P3 Continuously improve

- P3.1 A turn or prompt that worked MUST be savable as a skill in one action.
- P3.2 A member's correction to an agent MUST be offered as a skill update, so the next session does not need it.
- P3.3 A workflow that runs repeatedly MUST record its outcome (passed, failed, duration, cost) so changes to skills
  and models can be compared.

### P4 People are not routers

- P4.1 An agent MUST NOT ask a member a question that the workspace already answers: other sessions, artifacts, skills
  or connected systems. It searches first.
- P4.2 Status MUST come from the agent: what runs, what waits on whom, what shipped. No member chases another member
  for it.
- P4.3 A gate or question MUST reach exactly the members who can answer it, through the inbox and the surface they use.

### P5 Nothing starts from scratch

- P5.1 Every session MUST be resumable with its context: checkout, branch, agent conversation, box state.
- P5.2 Every artifact MUST link back to the session that made it, and opening it MUST be able to resume that session.
- P5.3 A new member MUST inherit the workspace's agents, skills, connectors and history on day one, with no setup of
  their own.

## Access

Access is a role, which grants groups of actions, plus what the member reaches. Everything runs on an environment (a
checkout is cut on one), so reach is decided there: an environment is open to everyone, or restricted to the admins and
the members admitted to it, and a member's access to a session MUST be the intersection of the two.

| Action group | Actions | Editor | Manager | Admin |
| --- | --- | --- | --- | --- |
| view | Read sessions, transcripts, artifacts, board | yes | yes | yes |
| drive | Start sessions, send messages, answer questions, stop | yes | yes | yes |
| approve | Decide gates on tools that act outside the box (push, pull request) | no | yes | yes |
| manage | Agents, tool levels, skills, connectors, environments, credentials | no | yes | yes |
| own | Members, roles, billing | no | no | yes |

Three roles, chosen Oct 3, 2026: editor, manager, admin. There is no viewer yet and no separate owner: the admin is
the owner, and the workspace keeps at least one (A6). An admin reaches every environment; an editor or manager reaches the
unrestricted ones and those they are admitted to (the Access tab of the environment form). A person sets the Access of the members they control, those of a lower role, and
keeps an environment they restrict themselves; the admins always reach it. The menus leave out the pages a role cannot open.

- A1 Every action MUST name one action group, and what it finds MUST stay within the environments the member reaches. No
  group means no route.
- A2 An environment is open to everyone unless restricted. A member MUST NOT see that a session on an environment out of
  their reach exists: not in lists, search, the inbox, notes or the live stream.
- A3 A gate MUST be decided only by a member who holds approve on the session's environment. The decision records who
  decided.
- A4 In a shared session, the agent MUST only use integrations that every participant can use. Adding a member with a
  narrower scope to a session MUST be refused or narrow the session.
- A5 A turn MUST run on a workspace credential, or on the credential of the member who wrote it. It MUST NOT use the
  session starter's personal credential for another member's turn.
- A6 The workspace MUST keep at least one admin.

## Security and governance

### S Security

- S1 The agent loop MUST run in nvoi's cloud, never on a member's laptop, because a laptop agent cannot be reached by
  teammates, stops when the laptop closes, and acts with the member's own access. A laptop MAY execute tool calls for a
  cloud session (the calls in a session's preview, docs/preview-browser.md), each gated like any other tool.
- S2 Agents reach the network only through their tools, each governed by the agent's levels; the box blocks the
  cloud's metadata address.
- S3 Every tool that sends data out of the workspace (outbound?) MUST default to approval. Setting one to auto MUST
  show the agent's unguarded warning.
- S4 Forge tokens MUST be minted per call, narrowed to one repository and the tool's permissions, and MUST NOT reach
  the box.
- S5 Credentials MUST be sealed at rest and MUST NOT appear in transcripts, logs or artifacts. The model credential
  on the box, which the agent needs to run, is the one accepted exception.

### G Governance

For any session, a year later, nvoi MUST answer four questions:

- G1 Which agent ran, with which model and which skills?
- G2 Who requested each turn, and who decided each gate?
- G3 What data did it access: tools called, connectors read, hosts reached?
- G4 What did it change: commits, pushes, pull requests, artifacts, external writes?
- G5 That record MUST be append-only and MUST survive archiving the session, the agent or the member.

### D Data and providers

- D1 A workspace MUST be able to run on more than one harness and model vendor, and switch per agent.
- D2 A workspace MUST be able to export its skills, transcripts, artifacts and outcome records in an open format.
- D3 nvoi MUST be self-hostable with narrowed capabilities: the core loop only (agents, skills, sessions on one
  customer-keyed box vendor, gates, GitHub, artifacts), with no vendor-only dependency. Billing, Apps hosting, the site
  and extra connectors are off.

## Status

Each rule is proved by a dogfood scenario (`packages/saas/dogfood`, the `dogfood` skill).
