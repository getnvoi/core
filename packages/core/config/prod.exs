import Config

# Do not print debug messages in production
config :logger, level: :info

# Runtime production configuration, including reading
# of environment variables, is done on config/runtime.exs.

# A live installation: its hosts' rules are enforced (NvoiWeb.Hosts), its names read from NVOI_HOST at boot
# (runtime.exs, Nvoi.Hosts). Every other build (dev, a stack, the tests) is not production.
config :nvoi, production: true

# Production is the only installation on its provider org and database account: OrphansJob stops and drops
# what no row holds. Everywhere else it only lists.
config :nvoi, orphans: :stop
