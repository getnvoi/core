defmodule Compute.NeonTest do
  # Neon's API answered by a stub: a production project found by its exact name with its main branch's variables,
  # none found, two refused, the main branch's size.
  use ExUnit.Case, async: false
  alias Compute.Database.Neon

  setup do
    Nvoi.ComputeEnv.put(:database_req, plug: {Req.Test, Compute.Database.Client})
    on_exit(fn -> Nvoi.ComputeEnv.put(:database_req, nil) end)
  end

  defp stub(projects) do
    test = self()

    Req.Test.stub(Compute.Database.Client, fn conn ->
      send(test, {:neon, conn.method, conn.request_path, conn.query_string})

      body =
        case conn.request_path do
          "/api/v2/users/me/organizations" ->
            %{organizations: [%{id: "org-1"}]}

          "/api/v2/projects" ->
            %{projects: projects}

          "/api/v2/projects/p-1/branches" ->
            %{
              branches: [
                %{id: "br-dev", default: false, logical_size: 1},
                %{id: "br-main", default: true, logical_size: 31_457_280}
              ]
            }

          "/api/v2/projects/p-1/endpoints" ->
            %{
              endpoints: [
                %{branch_id: "br-dev", type: "read_write", host: "ep-dev.neon.test"},
                %{branch_id: "br-main", type: "read_write", host: "ep-main.neon.test"}
              ]
            }

          "/api/v2/projects/p-1/branches/br-main/databases" ->
            %{databases: [%{name: "neondb", owner_name: "neondb_owner"}]}

          "/api/v2/projects/p-1/branches/br-main/roles/neondb_owner/reveal_password" ->
            %{password: "npg/secret"}
        end

      Req.Test.json(conn, body)
    end)
  end

  test "a project is found by its exact name, with its main branch's variables" do
    stub([
      %{id: "p-0", name: "nvoi-prod-acme-worker-production-2"},
      %{id: "p-1", name: "nvoi-prod-acme-worker-production"}
    ])

    assert %{ref: "p-1", env: env} = Neon.find("key", "nvoi-prod-acme-worker-production")
    assert_received {:neon, "GET", "/api/v2/projects", query}

    assert URI.decode_query(query) == %{
             "search" => "nvoi-prod-acme-worker-production",
             "org_id" => "org-1"
           }

    assert env["DATABASE_URL"] ==
             "postgresql://neondb_owner:npg%2Fsecret@ep-main.neon.test/neondb?sslmode=require"

    assert %{
             "PGHOST" => "ep-main.neon.test",
             "PGUSER" => "neondb_owner",
             "PGPASSWORD" => "npg/secret"
           } = env

    assert %{"PGDATABASE" => "neondb", "PGPORT" => "5432", "PGSSLMODE" => "require"} = env
    assert Neon.size("key", "p-1") == 31_457_280
  end

  test "none of that name is nil; two of that name are refused" do
    stub([%{id: "p-0", name: "nvoi-prod-acme-worker-production-2"}])
    assert Neon.find("key", "nvoi-prod-acme-worker-production") == nil

    stub([%{id: "p-1", name: "same"}, %{id: "p-2", name: "same"}])

    assert_raise Compute.Error, ~r/several projects named same/, fn ->
      Neon.find("key", "same")
    end
  end
end
