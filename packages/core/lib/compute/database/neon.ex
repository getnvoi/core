defmodule Compute.Database.Neon do
  @moduledoc "Postgres on Neon: a project per environment, a branch per worktree."
  @behaviour Compute.Database
  alias Compute.Database.Client

  @base "https://console.neon.tech/api/v2"
  @region "aws-eu-central-1"
  @min_cu 0.25

  def engine, do: "postgres"
  def label, do: "Postgres"
  def versions, do: ~w(14 15 16 17 18)
  def default_version, do: "17"

  defp client(key), do: %{base: @base, key: key, vendor: "neon", busy: [423]}

  def create(key, name, version, limits) do
    project =
      %{
        name: name,
        pg_version: String.to_integer(version),
        region_id: Compute.setting("neon_region") || @region,
        org_id: org(key),
        default_endpoint_settings: compute(limits),
        settings: %{quota: quota(limits)}
      }
      |> Map.reject(fn {_, v} -> is_nil(v) end)

    body = Client.post(client(key), "/projects", %{project: project})
    %{ref: body["project"]["id"], env: env(key, body, nil, nil)}
  end

  def branch(key, ref, name) do
    body =
      Client.post(client(key), "/projects/#{ref}/branches", %{
        branch: %{name: name},
        endpoints: [%{type: "read_write"}]
      })

    branch = body["branch"]["id"]
    %{ref: "#{ref}/#{branch}", env: env(key, body, ref, branch)}
  rescue
    e in Compute.Error ->
      if String.contains?(e.message, "BRANCHES_LIMIT"),
        do:
          raise(Compute.Database.Full,
            message: "the database has as many branches as Neon allows"
          ),
        else: reraise(e, __STACKTRACE__)
  end

  def drop(key, ref) do
    [project, branch] = String.split(ref, "/", parts: 2)
    Client.delete!(client(key), "/projects/#{project}/branches/#{branch}")
  end

  def destroy(key, ref), do: Client.delete!(client(key), "/projects/#{ref}")

  def limit(key, ref, limits) do
    Client.patch(client(key), "/projects/#{ref}", %{
      project: %{default_endpoint_settings: compute(limits), settings: %{quota: quota(limits)}}
    })

    for e <- Client.get(client(key), "/projects/#{ref}/endpoints")["endpoints"] || [],
        do:
          Client.patch(client(key), "/projects/#{ref}/endpoints/#{e["id"]}", %{
            endpoint: compute(limits)
          })
  end

  def branches(key, ref),
    do:
      for(
        b <- Client.get(client(key), "/projects/#{ref}/branches")["branches"] || [],
        !b["default"],
        do: "#{ref}/#{b["id"]}"
      )

  @doc "The project named `name` exactly, with its main branch's variables; nil when none is. Two of that name raise: neither is taken."
  def find(key, name) do
    query =
      URI.encode_query(Map.reject(%{search: name, org_id: org(key)}, fn {_, v} -> is_nil(v) end))

    case for(
           p <- Client.get(client(key), "/projects?#{query}")["projects"] || [],
           p["name"] == name,
           do: p
         ) do
      [] -> nil
      [project] -> %{ref: project["id"], env: main_env(key, project["id"])}
      _ -> raise Compute.Error, message: "neon holds several projects named #{name}"
    end
  end

  @doc "The logical size of the project's main branch, in bytes."
  def size(key, ref) do
    main =
      Enum.find(
        Client.get(client(key), "/projects/#{ref}/branches")["branches"] || [],
        & &1["default"]
      )

    main && main["logical_size"]
  end

  defp main_env(key, project) do
    branch =
      Enum.find(
        Client.get(client(key), "/projects/#{project}/branches")["branches"] || [],
        & &1["default"]
      ) ||
        raise(Compute.Error, message: "neon project #{project} has no main branch")

    endpoints =
      for e <- Client.get(client(key), "/projects/#{project}/endpoints")["endpoints"] || [],
          e["branch_id"] == branch["id"],
          e["type"] == "read_write",
          do: e

    env(key, %{"endpoints" => endpoints}, project, branch["id"])
  end

  defp compute(limits),
    do: %{
      autoscaling_limit_min_cu: @min_cu,
      autoscaling_limit_max_cu: max(limits["compute"], @min_cu)
    }

  defp quota(limits), do: %{logical_size_bytes: trunc(limits["storage"] * 1_000_000_000)}

  defp env(key, body, project, branch) do
    uri =
      get_in(body, ["connection_uris", Access.at(0), "connection_uri"]) ||
        uri(key, body, project, branch)

    parsed = URI.parse(uri)
    [user, password] = String.split(parsed.userinfo, ":", parts: 2)

    %{
      "DATABASE_URL" => uri,
      "PGHOST" => parsed.host,
      "PGPORT" => to_string(parsed.port || 5432),
      "PGUSER" => URI.decode_www_form(user),
      "PGPASSWORD" => URI.decode_www_form(password),
      "PGDATABASE" => String.trim_leading(parsed.path || "", "/"),
      "PGSSLMODE" => "require"
    }
  end

  defp uri(key, body, project, branch) do
    host =
      get_in(body, ["endpoints", Access.at(0), "host"]) ||
        raise(Compute.Error, message: "neon answered no endpoint")

    path = "/projects/#{project}/branches/#{branch}"
    databases = Client.get(client(key), "#{path}/databases")["databases"] || []

    database =
      Enum.find(databases, &(&1["name"] == "neondb")) || List.first(databases) ||
        raise(Compute.Error, message: "neon branch #{branch} has no database")

    password =
      Client.get(client(key), "#{path}/roles/#{database["owner_name"]}/reveal_password")[
        "password"
      ]

    "postgresql://#{database["owner_name"]}:#{URI.encode_www_form(password)}@#{host}/#{database["name"]}?sslmode=require"
  end

  defp org(key) do
    Compute.setting("neon_org_id") ||
      get_in(Client.get(client(key), "/users/me/organizations"), [
        "organizations",
        Access.at(0),
        "id"
      ])
  rescue
    Compute.Error -> nil
  end
end
