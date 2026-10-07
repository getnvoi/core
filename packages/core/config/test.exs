import Config

# Configure your database
#
# The MIX_TEST_PARTITION environment variable can be used
# to provide built-in test partitioning in CI environment.
# Run `mix help test` for more information.
config :nvoi, Nvoi.Postgres,
  hostname: "localhost",
  # Each worktree its own: another branch's migrations never reach this one's schema.
  database:
    "nvoi_test_#{Path.expand("../../..", __DIR__) |> Path.basename() |> String.replace(~r/[^a-z0-9_]/, "_")}#{System.get_env("MIX_TEST_PARTITION")}",
  pool: Ecto.Adapters.SQL.Sandbox,
  pool_size: System.schedulers_online() * 2

# We don't run a server during test. If one is required,
# you can enable the server option below.
config :nvoi, NvoiWeb.Endpoint,
  http: [ip: {127, 0, 0, 1}, port: 4002],
  secret_key_base: "zh5j8mOGIMtsDU9r3EcnqZnlCjpDkrGF2VdTGPBnDSwVPmqvXi5ojP5VQkDYyeZl",
  server: false

# The credentials tests read: stand-ins, never the sealed file (CI holds no usable key). Not secrets.
# packages/saas adds its own (Stripe) to these; a GitHub test puts the App's (Nvoi.GitHubFixtures).
config :nvoi, :credentials, %{
  "secret_key_base" => "test-secret-key-base-not-a-secret-0000000000000000000000000000000000",
  "gate_jwt_secret" => "test-gate-secret-not-a-secret",
  "encryption" => %{
    "primary_key" => "test-primary-key-not-a-secret-0000000000",
    "key_derivation_salt" => "test-salt-not-a-secret-000000000000000"
  }
}

# Print only warnings and errors during test
config :logger, level: :warning

# Initialize plugs at runtime for faster test compilation
config :phoenix, :plug_init_mode, :runtime

# The preview worker's operations answer in the tests; Nvoi.PreviewsTest turns them off.
config :nvoi, :previews, true

config :nvoi, :storage,
  service: Nvoi.Storage.Disk,
  name: "test",
  root: Path.expand("../tmp/storage", __DIR__)

config :nvoi, Oban, testing: :manual
# Signals go out at once, in the test's own process, instead of grouped by a partition.
config :nvoi, Nvoi.Live, every: 0

# The tests' writes never commit, so the database sends no notice; a test listens itself (TurnNoticesTest).
config :nvoi, Nvoi.Live.Listener, listen: false
config :nvoi, contract: true

config :nvoi, sandbox_host: "sandbox.nvoi.to", preview_suffix: "-preview.nvoi.to"

# The vendors' login servers answered by each test's stub (Req.Test), never the network.
config :nvoi, Llm.Login, plug: {Req.Test, Llm.Login}

# Activity rows wait until a test flushes them (Nvoi.Activity.Buffer.flush/0).
config :nvoi, Nvoi.Activity.Buffer, flush_interval: nil
