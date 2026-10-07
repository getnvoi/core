defmodule Nvoi.Application do
  # See https://hexdocs.pm/elixir/Application.html
  # for more information on OTP Applications
  @moduledoc false

  use Application

  @impl true
  def start(_type, _args) do
    # The preview edge's per-minute budgets (NvoiWeb.EdgeController), owned by the application.
    Nvoi.Observe.attach()
    Nvoi.Activity.Recorder.attach()
    :ets.new(NvoiWeb.EdgeController, [:named_table, :public, :set, write_concurrency: true])
    # A task webhook's calls per hour (NvoiWeb.Hooks.TasksController).
    :ets.new(NvoiWeb.Hooks.TasksController, [:named_table, :public, :set, write_concurrency: true])

    children = [
      NvoiWeb.Telemetry,
      Nvoi.Postgres,
      Nvoi.Activity.Buffer,
      # Before Oban runs anything: the jobs this node left executing in its previous life.
      %{id: :rescue, start: {Nvoi.Jobs.Rescue, :own, []}, restart: :temporary},
      {Oban, oban()},
      {DNSCluster, query: Application.get_env(:nvoi, :dns_cluster_query) || :ignore},
      {Phoenix.PubSub, name: Nvoi.PubSub},
      Nvoi.Live,
      Nvoi.Live.Listener,
      NvoiWeb.Presence,
      Nvoi.Runs,
      NvoiWeb.Endpoint
    ]

    # See https://hexdocs.pm/elixir/Supervisor.html
    # for other strategies and supported options
    opts = [strategy: :one_for_one, name: Nvoi.Supervisor]
    Supervisor.start_link(children, opts)
  end

  # Tell Phoenix to update the endpoint configuration
  # whenever the application is updated.
  # Jobs run in the server only: a one-off `mix run` must not pick one up and exit under it.
  defp oban do
    config =
      Application.fetch_env!(:nvoi, Oban)
      |> Keyword.update!(:plugins, fn plugins ->
        Enum.map(plugins, fn
          {Oban.Plugins.Cron, opts} ->
            {Oban.Plugins.Cron, Keyword.put(opts, :crontab, Nvoi.Schedule.crontab())}

          plugin ->
            plugin
        end)
      end)

    if Application.get_env(:phoenix, :serve_endpoints) ||
         Application.get_env(:nvoi, NvoiWeb.Endpoint)[:server],
       do: config,
       else: Keyword.merge(config, queues: false, plugins: false)
  end

  @impl true
  def config_change(changed, _new, removed) do
    NvoiWeb.Endpoint.config_change(changed, removed)
    :ok
  end
end
