import Config

# config/runtime.exs is executed for all environments, including
# during releases. It is executed after compilation and before the
# system starts, so it is typically used to load production configuration
# and secrets from environment variables or elsewhere. Do not define
# any compile-time configuration in here, as it won't be applied.
# The block below contains prod specific runtime configuration.

# ## Using releases
#
# If you use `mix release`, you need to explicitly enable the server
# by passing the PHX_SERVER=true when you start it:
#
#     PHX_SERVER=true bin/nvoi start
#
# Alternatively, you can use `mix phx.gen.release` to generate a `bin/server`
# script that automatically sets the env var above.
# NVOI_READ_ONLY=1 (bin/monitoring's console) never serves, whatever PHX_SERVER says: no endpoint, so no jobs, no
# turns resumed, no seeds synced.
if System.get_env("PHX_SERVER") && System.get_env("NVOI_READ_ONLY") not in ~w(1 true) do
  config :nvoi, NvoiWeb.Endpoint, server: true
end

if config_env() == :prod do
  # The sealed credentials ship in nvoi.to's image (/app/config); NVOI_CREDENTIALS_KEY opens them. Set before
  # anything reads a secret (Nvoi.Credentials keeps what it first read).
  config :nvoi, credentials_dir: System.get_env("NVOI_CREDENTIALS_DIR") || "/app/config"

  Application.put_env(
    :nvoi,
    :credentials_dir,
    System.get_env("NVOI_CREDENTIALS_DIR") || "/app/config"
  )

  read_only = System.get_env("NVOI_READ_ONLY") in ~w(1 true)

  # DATABASE_URL may reach Postgres through PgBouncer in transaction mode (nvoi.to's `nvoi-db-pooler-rw` Pooler):
  # no named prepared statements there. DIRECT_DATABASE_URL reaches Postgres itself, for what needs one session for
  # its whole life: the notices listener's LISTEN (Nvoi.Live.Listener) and the migrations' lock. Without them, the
  # URL is built from DATABASE_HOST (Nvoi.Postgres.urls/1).
  %{url: database_url, direct: direct_database_url} =
    if read_only && System.get_env("READER_DATABASE_URL"),
      do: Nvoi.Postgres.urls(%{"DATABASE_URL" => System.get_env("READER_DATABASE_URL")}),
      else: Nvoi.Postgres.urls()

  maybe_ipv6 = if System.get_env("ECTO_IPV6") in ~w(true 1), do: [:inet6], else: []

  config :nvoi, direct_database_url: direct_database_url

  config :nvoi, Nvoi.Postgres,
    # ssl: true,
    url: database_url,
    prepare: :unnamed,
    pool_size: String.to_integer(System.get_env("POOL_SIZE") || "10"),
    # For machines with several cores, consider starting multiple pools of `pool_size`
    # pool_count: 4,
    socket_options: maybe_ipv6

  # The secret key base is used to sign/encrypt cookies and other secrets.
  # A default value is used in config/dev.exs and config/test.exs but you
  # want to use a different value for prod and you most likely don't want
  # to check this value into version control, so we use an environment
  # variable instead.
  # NVOI_READ_ONLY=1 is `bin/monitoring console`: a second VM in the pod that reads. It connects as the read-only
  # role, so Postgres refuses any write, and it opens no credential, so it reaches no provider, forge or payment
  # service. With no endpoint served, it runs no job and resumes no turn.
  if read_only, do: config(:nvoi, :credentials, %{})

  # Every required secret, named at once when any is missing (Nvoi.Credentials).
  if not read_only, do: Nvoi.Credentials.check!()

  secret_key_base =
    if read_only,
      do: Base.encode64(:crypto.strong_rand_bytes(48)),
      else: System.get_env("SECRET_KEY_BASE") || Nvoi.Credentials.fetch!(["secret_key_base"])

  # The binaries put on every machine, built into the image rather than with Go at runtime.
  config :nvoi,
    boxd_binary: System.get_env("NVOI_BOXD_BINARY") || "/app/bin/linux/boxd",
    # The commit the image was built from (`GET /version`).
    version: System.get_env("VERSION")

  # The Erlang node's own name on the jobs it runs, so Nvoi.Jobs.Rescue can tell a live node's jobs from a gone one's.
  config :nvoi, Oban, node: to_string(node())

  # The name the installation answers on; the others beside it (Nvoi.Hosts: git, MCP, the preview worker's, the
  # previews').
  host =
    System.get_env("NVOI_HOST") ||
      raise("NVOI_HOST is not set: the name this installation answers on")

  config :nvoi, Nvoi.Hosts.from(host)
  port = String.to_integer(System.get_env("PORT") || "4000")

  config :nvoi, :dns_cluster_query, System.get_env("DNS_CLUSTER_QUERY")

  config :nvoi, NvoiWeb.Endpoint,
    url: [host: host, port: 443, scheme: "https"],
    http: [
      # Enable IPv6 and bind on all interfaces.
      # Set it to  {0, 0, 0, 0, 0, 0, 0, 1} for local network only access.
      # See the documentation on https://hexdocs.pm/bandit/Bandit.html#t:options/0
      # for details about using IPv6 vs IPv4 and loopback vs public addresses.
      ip: {0, 0, 0, 0, 0, 0, 0, 0},
      port: port
    ],
    secret_key_base: secret_key_base

  # ## SSL Support
  #
  # To get SSL working, you will need to add the `https` key
  # to your endpoint configuration:
  #
  #     config :nvoi, NvoiWeb.Endpoint,
  #       https: [
  #         ...,
  #         port: 443,
  #         cipher_suite: :strong,
  #         keyfile: System.get_env("SOME_APP_SSL_KEY_PATH"),
  #         certfile: System.get_env("SOME_APP_SSL_CERT_PATH")
  #       ]
  #
  # The `cipher_suite` is set to `:strong` to support only the
  # latest and more secure SSL ciphers. This means old browsers
  # and clients may not be supported. You can set it to
  # `:compatible` for wider support.
  #
  # `:keyfile` and `:certfile` expect an absolute path to the key
  # and cert in disk or a relative path inside priv, for example
  # "priv/ssl/server.key". For all supported SSL configuration
  # options, see https://hexdocs.pm/plug/Plug.SSL.html#configure/1
  #
  # We also recommend setting `force_ssl` in your config/prod.exs,
  # ensuring no data is ever sent via http, always redirecting to https:
  #
  #     config :nvoi, NvoiWeb.Endpoint,
  #       force_ssl: [hsts: true]
  #
  # Check `Plug.SSL` for all available options in `force_ssl`.
end
