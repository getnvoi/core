defmodule Nvoi.Builder.Daytona do
  @moduledoc """
  A build machine on nvoi's machine provider (the one agents' machines run on), found by its name: running, it is
  used; stopped, it is started; absent, it is made under that name (the provider's create adopts a machine another
  server made meanwhile) and boxd installed. It is prepared once, as a seed is (`Nvoi.Steps.prepare!`, docker
  included), and a mark on its disk says so, so a lost row never prepares it again. It holds a commit's source and
  docker's cache, never a credential, a production variable or a cluster's key: it uploads the image with a URL
  signed for that one object.
  """
  @behaviour Nvoi.Builder.Sandbox
  alias Compute.Box
  alias Nvoi.{Machines, Postgres}

  @prepared "/mnt/data/builds/.prepared"

  @impl true
  def up!(builder, say) do
    {module, key} = provider = Machines.provider(builder.provider_name)

    machine =
      case module.find(key, builder.name) do
        %{state: :running} = found ->
          found

        nil ->
          say.("machine", "making #{builder.name}")

          made =
            module.create(
              key,
              builder.name,
              Machines.labels(%{"nvoi-builder" => builder.environment_id}),
              module.default_shape(),
              Compute.SandboxVm.default_disk(provider, module.default_shape())
            )

          Machines.bootstrap!(provider, made, builder.name)
          made

        found ->
          say.("machine", "starting #{builder.name}")
          module.wake(key, found)
      end

    Postgres.update!(
      Ecto.Changeset.change(builder, box_id: machine.id, used_at: DateTime.utc_now())
    )

    box = Machines.box(provider, machine, builder.name)

    if Box.read(box, @prepared) == :missing do
      say.("machine", "installing docker on #{builder.name}")
      Nvoi.Steps.prepare!(box, fn _, _ -> :ok end)
      Box.put(box, @prepared, "1")
    end

    box
  end

  @impl true
  def put!(box, path, bytes) do
    Box.put(box, path, bytes)
    :ok
  end

  @impl true
  def run(box, script, on_line) do
    %{"id" => id} = Box.start(box, ["bash", "-c", script])
    follow(box, id, 0, on_line)
  end

  # The log read again from the last offset when the connection drops, until the command ends.
  defp follow(box, id, from, on_line) do
    offset = :counters.new(1, [])
    :counters.put(offset, 1, from)

    result =
      Box.follow(box, id, from, fn line, at ->
        :counters.put(offset, 1, at)
        on_line.(line)
      end)

    case result do
      {:exit, code} -> code
      {:closed, _} -> follow(box, id, :counters.get(offset, 1), on_line)
      _ -> Box.status(box, id)["exit_code"] || 1
    end
  end

  @impl true
  def park!(builder) do
    {module, key} = Machines.provider(builder.provider_name)
    if builder.box_id, do: module.halt(key, Machines.machine(builder.box_id, builder.name))
    :ok
  end
end
