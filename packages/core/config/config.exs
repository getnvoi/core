# This file is responsible for configuring your application
# and its dependencies with the aid of the Config module.
#
# This configuration file is loaded before any dependency and
# is restricted to this project.

# General application configuration
import Config

config :nvoi,
  ecto_repos: [Nvoi.Postgres],
  generators: [timestamp_type: :utc_datetime, binary_id: true]

# Configures the endpoint
config :nvoi, NvoiWeb.Endpoint,
  url: [host: "localhost"],
  adapter: Bandit.PhoenixAdapter,
  render_errors: [
    formats: [json: NvoiWeb.ErrorJSON],
    layout: false
  ],
  pubsub_server: Nvoi.PubSub,
  live_view: [signing_salt: "KIp112et"]

# The host that is the MCP server (NvoiWeb.Router), and the one that is git for hosted repositories.
config :nvoi, mcp_host: "mcp.nvoi.to"

config :nvoi, git_host: "git.nvoi.to"

# Configures Elixir's Logger
config :logger, :default_formatter,
  format: "$time $metadata[$level] $message\n",
  metadata: [:request_id, :tool, :actor, :user_id, :workspace_id, :agent_session_id]

# Use Jason for JSON parsing in Phoenix
config :phoenix, :json_library, Jason
# The socket is signed in by the API token in its connect params; logs never show it.
config :phoenix, :filter_parameters, ["password", "token"]

# Import environment specific config. This must remain at the bottom
# of this file so it overrides the configuration defined above.
import_config "#{config_env()}.exs"

# The migrations' versions, those structure.sql holds included, are in ecto_migrations.
config :nvoi, Nvoi.Postgres,
  priv: "priv/repo",
  migration_source: "ecto_migrations",
  # The core's own, wherever mix runs (packages/saas runs it too).
  dump_path: Path.expand("../priv/repo/structure.sql", __DIR__),
  migration_primary_key: [type: :binary_id]

config :elixir, :time_zone_database, Tzdata.TimeZoneDatabase

config :nvoi, Oban,
  repo: Nvoi.Postgres,
  # Machine starts and stops mostly wait on the provider: many at once, so a start never queues behind others.
  # Production: clusters made and destroyed, deployments built and rolled out, each on SSH for minutes.
  queues: [default: 10, machines: 100, production: 20],
  # The node's name on the jobs it runs: the pod's in production (runtime.exs), the host's elsewhere.
  node: System.get_env("NVOI_NODE") || to_string(elem(:inet.gethostname(), 1)),
  # Signals between nodes through the Erlang cluster (:pg), not Postgres' LISTEN, which the pooler cannot carry.
  notifier: Oban.Notifiers.PG,
  plugins: [
    {Oban.Plugins.Pruner, max_age: 7 * 24 * 3600},
    # Its entries are the schedule's (Nvoi.Schedule), set when Oban starts.
    {Oban.Plugins.Cron, crontab: []}
  ]

# What runs on a schedule, by name (Nvoi.Schedule): an edition's config adds its own entries, merged with these.
config :nvoi, :schedule,
  idle: {"* * * * *", Nvoi.Jobs.IdleJob},
  tasks: {"* * * * *", Nvoi.Jobs.TasksJob},
  prices: {"0 6 * * *", Nvoi.Jobs.PricesJob},
  snapshots: {"*/5 * * * *", Nvoi.Jobs.SnapshotsJob},
  orphans: {"0 4 * * *", Nvoi.Jobs.OrphansJob},
  storage: {"0 5 * * *", Nvoi.Jobs.StorageJob},
  storage_cost: {"50 23 * * *", Nvoi.Jobs.StorageCostJob},
  llm_connection: {"*/5 * * * *", Nvoi.Jobs.LlmConnectionJob},
  tool_grants: {"20 * * * *", Nvoi.Jobs.ToolGrantsJob},
  # Jobs a stopped node left executing (Nvoi.Jobs.Rescue).
  rescue: {"*/5 * * * *", Nvoi.Jobs.RescueJob}

config :nvoi, Compute,
  providers: [{"daytona", Compute.SandboxVm.Daytona}],
  database_vendors: [{"neon", Compute.Database.Neon}, {"turso", Compute.Database.Turso}],
  credentials: &Nvoi.Credentials.setting/1,
  price_table: Nvoi.Price
