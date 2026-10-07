defmodule Compute.Database.Turso do
  @moduledoc "SQLite on Turso: a database per environment, seeded copies per worktree."
  @behaviour Compute.Database
  alias Compute.Database.Client

  @base "https://api.turso.tech/v1"
  @group "default"

  def engine, do: "sqlite"
  def label, do: "SQLite"
  def versions, do: ["libsql"]
  def default_version, do: "libsql"

  defp client(key), do: %{base: @base, key: key, vendor: "turso"}

  def create(key, name, _version, _limits) do
    wake(key)
    Client.post(client(key), "#{org(key)}/databases", %{name: short(name), group: @group})
    %{ref: short(name), env: env(key, short(name))}
  end

  def branch(key, ref, name) do
    wake(key)
    child = short("#{ref}-#{name}")

    Client.post(client(key), "#{org(key)}/databases", %{
      name: child,
      group: @group,
      seed: %{type: "database", name: ref}
    })

    %{ref: child, env: env(key, child)}
  end

  def drop(key, ref), do: Client.delete!(client(key), "#{org(key)}/databases/#{ref}")

  def destroy(key, ref) do
    for branch <- branches(key, ref), do: drop(key, branch)
    drop(key, ref)
  end

  def limit(_key, _ref, _limits), do: nil

  def branches(key, ref),
    do:
      for(
        db <- Client.get(client(key), "#{org(key)}/databases")["databases"] || [],
        get_in(db, ["parent", "name"]) == ref,
        do: db["Name"]
      )

  defp env(key, name) do
    host = Client.get(client(key), "#{org(key)}/databases/#{name}")["database"]["Hostname"]

    jwt =
      Client.post(
        client(key),
        "#{org(key)}/databases/#{name}/auth/tokens?authorization=full-access"
      )["jwt"]

    %{"DATABASE_URL" => "libsql://#{host}", "DATABASE_AUTH_TOKEN" => jwt}
  end

  defp wake(key) do
    Client.post(client(key), "#{org(key)}/groups/#{@group}/unarchive")
  rescue
    e in Compute.Error ->
      if e.status == 400 and String.contains?(e.message, "not archived"),
        do: nil,
        else: reraise(e, __STACKTRACE__)
  end

  defp short(name),
    do:
      name
      |> String.downcase()
      |> String.replace(~r/[^a-z0-9-]/, "-")
      |> String.slice(0, 64)
      |> String.trim_trailing("-")

  defp org(key),
    do:
      "/organizations/" <>
        (Compute.setting("turso_org") || hd(Client.get(client(key), "/organizations"))["slug"])
end
