# Tools: one declaration per operation

Every operation of nvoi is a tool: one module that says everything about itself once. The router, the API document,
the CLI, the agents' gateway, the role checks, parameter validation and observability are all generated from those
declarations, so none of them can drift from the code. Activities (a row per call) are one more consumer of the
same declarations and the same events.

## The declaration

```elixir
defmodule Nvoi.Tools.Agent.Update do
  @moduledoc "Changes what is sent; tool levels sent are merged over the stored ones."
  use Nvoi.Tools.Action,
    group: "agent",
    action: "update",
    summary: "Update an agent",
    patch: "/agents/{id}",
    access: :mutation,
    locked: true,
    default_role: :manage,
    refuses: [422],
    answer: [agent: NvoiWeb.AgentResource]

  param :id, :uuid, in: :path
  param :name, :string
  param :tagline, :string
  param :instructions, :string
  param :model, :string
  param :llm_config_id, :string
  param :tools, {:map, :string}
  param :skill_ids, {:list, :uuid}

  def call(params, current), do: ...
end
```

The Rust port's names, one for one (`crates/macros/src/tool_attribute.rs`, `crates/resources/src/tool_meta.rs`):

- `group` and `action`: the CLI's `nvoi <group> <action>` and the API document's tag; `name` defaults to
  `<group>_<action>`, the operation id and the name an agent calls it by.
- `summary`, and for the transcript `label` and `icon`; `target`, the input that is a call's one line.
- `category`: the one axis the lists of tools are grouped on (`Nvoi.Tools.Category`): the record the tool's entry
  point names, or what the CLI's own tools do (files, shell, web, planning, subagents). Declared on every tool, never
  derived; a test fails on a tool without one. Served as `category` in place of the module-derived `area`.
- `get` / `post` / `put` / `patch` / `delete`: the path under `/api/v1`, with `{name}` for what it names.
- `status` (200 unless said; 201 created, 204 no body), `also` (other successes with the same body, e.g. 202),
  `refuses` (the refusals it answers), `refuses_with_code` (refusals said as a sentence and a code where the status
  otherwise names fields), `skip_authentication` (the login).
- `access` (`:free`, `:mutation`, `:sensitive`) and `default_role` (`:view`, `:drive`, `:approve`, `:manage`,
  `:own`): Rust's `role`, what the server calls `group` today. It is the tool's default: a workspace changes, per role
  and per member, which tools run, ask or are denied (`ToolPreset`, `ToolGrant`).
- `role_overridable` (`true` unless said): whether a workspace may change it; `false` keeps the default whatever is
  set. Distinct from `locked`, which keeps an agent on the tool's default level.
- `answer` (the envelope: key → resource) and `answers` (other statuses whose body is an answer, not a refusal:
  `answers: [{404, ...}]`).
- `level`: its own level when no agent says (built-in and browser tools).
- Flags: `reads` (a sensitive action that reads: an agent gets it masked), `locked` (its default level is the only
  one), `asks` (waits on a person at every level but deny), `outbound` (sends data out of the workspace),
  `runs_on_machine` (approved while the machine is down, it waits for it), `internal` (one dimmed line),
  `production` (it reaches a production environment, its cluster or the workspace's cloud: the gateway never calls
  it, so no agent does).
- `kind`: `:action` (an API operation), `:nvoi` (one of nvoi's tools an agent calls), `:builtin` (the CLI's own the
  levels govern), `:browser` (a call in the session's preview), `:workflow` (a step tool).
- `param name, type, in:, required:, null:, doc:, values:` — types `:string`, `:integer`, `:number`, `:boolean`,
  `:uuid`, `:file`, `{:list, type}`, `{:map, type}`, `:map`; `in: :path` for what the path names; `values:` for an
  enumeration; `null: true` when null is a value (a required one must then be sent, null or not). An absent param is
  absent; a param sent as null is null (Rust's present-field). A `do` block declares the fields of a `:map` or a
  `{:list, :map}`: `param :steps, {:list, :map} do param :name, :string, required: true ... end`.
- A resource declares its fields the same way (`field :name, :string, null: true, doc:`), with the resources it
  embeds, so every answer's schema is generated too.

## What is generated from it

1. **The router**: every declared operation, in a stable order; `NvoiWeb.Router.tools/0` stays.
2. **The API document**: `mix nvoi.openapi` writes `priv/openapi.yaml` (and the CLI's `openapi.json`) from the
   declarations: paths, operation ids (`<group>_<action>` as the CLI reads them), parameters, request bodies,
   answers and refusals, examples kept from today's document. CI fails when the committed document differs from
   the generated one; nobody edits it by hand again.
3. **Parameter validation**, before `call/2`: enumerations checked (422 `invalid_<name>`). `required` is documented,
   not refused here: the first document marked params required that the actions did not refuse (a process stopped
   without a signal takes the default, an env var may be set empty, a version uploads a file without content, a
   blank name is a 422 under its field), so each body keeps refusing as it did. Required becomes the dispatcher's
   once each action's declaration is checked against its body.
   `call/2` receives clean params.
4. **The gateway**: `describe` answers the declaration (summary, params with their docs, record, refusals), so an
   agent sees exactly what a person's client sees.
5. **The checks**: the role (`group`) and access as today, plus, in development and tests, the answer checked
   against its declared resource (the contract check, now against the code's own declaration).

## Observability, built in

Every call passes through one dispatcher, so it is observed in one place, with the declaration's names:

- **Telemetry spans** (`:telemetry`), one family per kind of work, each with the same metadata — the tool, its
  group and access, the actor (`person`, `agent` with the agent and the session, `system` for jobs), the user and
  the workspace, the record touched, the status, the refusal's code, the duration:
  - `[:nvoi, :tool, :call]` — every API, CLI, desktop and gateway call;
  - `[:nvoi, :agent_tool, :call]` — nvoi's tools an agent calls inside a turn (MCP);
  - `[:nvoi, :turn, …]` — a turn's start, hold, resume, end (the runner);
  - `[:nvoi, :box, :request]` and `[:nvoi, :provider, :request]` — every call to a machine (boxd) and to a vendor
    (Daytona, Neon, Turso, GitHub, Forgejo, Cloudflare), with the endpoint and the status;
  - Oban's own job telemetry, tagged with the same actor and record.
- **Logger metadata** set per call (request id, tool, actor, workspace, record), so every log line a call causes —
  Ecto's, a provider's, a box's — carries who and what; one structured line per call when it ends, with its outcome.
- **One handler module** (`Nvoi.Observe`) attaches to these events: the log line, the dev console's summary, metrics
  (counts and durations per tool, refusals per code, provider errors per vendor). Activities are one more handler
  on the same events (`Nvoi.Activity.Recorder`): a row per call, no change to any tool.

## How we get there

1. A script reads today's `priv/openapi.yaml` and writes the declarations into the ~145 action modules and the ~30
   resources, so the generated document is the same as today's on day one: the diff between the generated and the
   committed document is empty, operation by operation.
2. `Nvoi.Tools.Action` gains `param`, the dispatcher's validation and the telemetry span; the router reads the
   declarations; `mix nvoi.openapi` generates; CI compares.
3. Agent tools move to `param`; the gateway describes from declarations.
4. `Nvoi.Observe` with the log line and metrics; the box, provider, turn and job spans.
5. The contract check becomes a test on declared answers; the hand-edited document is gone.

After the branches in flight are merged (artifacts, browser tools, workflows): every action file changes.

## Activities

One log of what happened (`activities`: workspace, user, record, event, level, source, properties, duration, path,
time), read as funnels. No action records itself:

- `Nvoi.Activity.Recorder` turns `[:nvoi, :tool, :call, :stop | :exception]` into one row per call: the tool's name
  as its event, `source` `person` or `agent` (the agent and its session in the properties), the record its path or
  its creation names (`record_type` is the tool's group), a refusal or a failure as level `error` with the status and
  code the API answered. A call outside any workspace, and the `activity` and `funnel` tools, write none.
- The jobs' events go through `Nvoi.Activity.system/3`, which emits `[:nvoi, :system, :event]`; the recorder writes
  them as `source: "system"`: `machine.paused` (idle or out of credit), `seed.built`, `task.fired`,
  `credit.ran_out`.
- The desktop's own events come through `activity_ingest`, checked against `Nvoi.Activity.Catalog`
  (`source: "desktop"`).
- Every row is cast to `Nvoi.Activity.Buffer`, one process that writes them together (every 5 s, or 500 at a time):
  the call never waits on the database, a stalled writer drops rows past its limit, a failed write is logged.
- Funnels (`funnel_*`, nvoi's staff only: `system_users`) read the table: sequences of steps within a window, per
  member or per record, with conversions, attempts and errors, a trend, workspaces and the actors behind a step;
  retention grids of first use. Seeded ones live in `priv/funnels/*.yml`.
