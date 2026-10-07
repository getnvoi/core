defmodule Nvoi.ServiceTest do
  # What a production environment runs: set by name, listed, removed; a development one has none.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.Tools
  alias Nvoi.Tools.Action

  setup do
    place()
  end

  defp set(ctx, params),
    do: Action.run(Tools.Service.Set, Map.put(params, "id", ctx.production.id), ctx.person)

  test "a worker at 0 replicas, stopped before its next starts; set again changes only what is sent",
       ctx do
    assert %{service: worker} =
             set(ctx, %{
               "name" => "worker",
               "command" => "bun run src/worker.ts",
               "replicas" => 0,
               "strategy" => "Recreate",
               "memory" => "512Mi",
               "cpu" => "500m"
             })

    assert worker.replicas == 0 and worker.strategy == "Recreate" and worker.image == nil

    assert %{service: %{replicas: 1, command: "bun run src/worker.ts", memory: "512Mi"}} =
             set(ctx, %{"name" => "worker", "replicas" => 1})

    assert %{service: %{memory: nil}} = set(ctx, %{"name" => "worker", "memory" => ""})

    set(ctx, %{"name" => "cache", "image" => "redis:7-alpine", "port" => 6379})

    assert %{
             services: [
               %{name: "cache", strategy: "RollingUpdate", port: 6379},
               %{name: "worker"}
             ]
           } =
             Action.run(Tools.Service.List, %{"id" => ctx.production.id}, ctx.person)

    assert Action.run(
             Tools.Service.Delete,
             %{"id" => ctx.production.id, "name" => "cache"},
             ctx.person
           ) == nil

    assert %{services: [%{name: "worker"}]} =
             Action.run(Tools.Service.List, %{"id" => ctx.production.id}, ctx.person)
  end

  test "names, replicas, strategies and quantities Kubernetes would refuse are refused", ctx do
    for {params, field} <- [
          {%{"name" => "Worker_1"}, :name},
          {%{"name" => "w", "replicas" => -1}, :replicas},
          {%{"name" => "w", "memory" => "lots"}, :memory},
          {%{"name" => "w", "cpu" => "two"}, :cpu},
          {%{"name" => "w", "port" => 70_000}, :port}
        ] do
      error = assert_raise Ecto.InvalidChangesetError, fn -> set(ctx, params) end
      assert error.changeset.errors[field], inspect(params)
    end

    assert %Action.Refused{code: "invalid_strategy"} =
             catch_error(set(ctx, %{"name" => "w", "strategy" => "BlueGreen"}))
  end

  test "a development environment has no services", ctx do
    assert %Action.Refused{code: "not_production"} =
             catch_error(
               Action.run(
                 Tools.Service.Set,
                 %{"id" => ctx.development.id, "name" => "web"},
                 ctx.person
               )
             )
  end
end
