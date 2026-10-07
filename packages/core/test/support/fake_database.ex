defmodule Compute.FakeDatabase do
  @moduledoc """
  A Postgres vendor for the app's tests, served as `neon`: projects held by name in an Agent, each with its variables.
  `use/0` makes it the vendor and sets a stand-in key; `projects/0` reads what it holds, `put/2` adds one as if made
  before, `fail/1` makes the next create raise.
  """
  @behaviour Compute.Database

  def use do
    {:ok, pid} = Agent.start(fn -> %{projects: %{}, fail: nil} end)
    vendors = Compute.env(:database_vendors)
    Nvoi.ComputeEnv.put(:database_vendors, [{"neon", __MODULE__}])
    Application.put_env(:nvoi, __MODULE__, pid)
    System.put_env("NEON_API_KEY", "neon-test-key")

    ExUnit.Callbacks.on_exit(fn ->
      Nvoi.ComputeEnv.put(:database_vendors, vendors)
      Application.delete_env(:nvoi, __MODULE__)
      System.delete_env("NEON_API_KEY")
    end)

    pid
  end

  defp pid, do: Application.fetch_env!(:nvoi, __MODULE__)

  @doc "Every project held: `%{name => %{ref, env, limits}}`."
  def projects, do: Agent.get(pid(), & &1.projects)

  def put(name, ref), do: Agent.update(pid(), &put_in(&1, [:projects, name], made(name, ref)))
  def fail(message), do: Agent.update(pid(), &%{&1 | fail: message})

  def env(name),
    do: %{
      "DATABASE_URL" =>
        "postgresql://owner:npg_password_of_#{name}@ep-#{name}.neon.test/neondb?sslmode=require",
      "PGHOST" => "ep-#{name}.neon.test",
      "PGPORT" => "5432",
      "PGUSER" => "owner",
      "PGPASSWORD" => "npg_password_of_#{name}",
      "PGDATABASE" => "neondb",
      "PGSSLMODE" => "require"
    }

  defp made(name, ref), do: %{ref: ref, env: env(name), limits: nil}

  def engine, do: "postgres"
  def label, do: "Postgres"
  def versions, do: ~w(17 18)
  def default_version, do: "17"

  def create("neon-test-key", name, _version, limits) do
    Agent.get_and_update(pid(), fn state ->
      if state.fail,
        do: {{:error, state.fail}, %{state | fail: nil}},
        else:
          (
            project = %{made(name, "p-#{map_size(state.projects) + 1}") | limits: limits}
            {{:ok, project}, put_in(state, [:projects, name], project)}
          )
    end)
    |> case do
      {:ok, project} -> Map.take(project, [:ref, :env])
      {:error, message} -> raise Compute.Error, message: message
    end
  end

  def find("neon-test-key", name),
    do: (project = projects()[name]) && Map.take(project, [:ref, :env])

  def limit("neon-test-key", ref, limits),
    do:
      Agent.update(pid(), fn state ->
        update_in(state.projects, fn projects ->
          Map.new(projects, fn {n, p} ->
            {n, if(p.ref == ref, do: %{p | limits: limits}, else: p)}
          end)
        end)
      end)

  def size("neon-test-key", _ref), do: 31_457_280

  def destroy("neon-test-key", ref),
    do:
      Agent.update(pid(), fn state ->
        update_in(state.projects, &Map.reject(&1, fn {_, p} -> p.ref == ref end))
      end)

  def branch(_key, _ref, _name), do: raise("a production database is never branched")
  def drop(_key, _ref), do: :ok
  def branches(_key, _ref), do: []
end
