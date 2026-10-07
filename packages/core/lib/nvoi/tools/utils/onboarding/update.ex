defmodule Nvoi.Tools.Utils.Onboarding.Update do
  @moduledoc """
  The repository's environment, made or changed in one call: `provider_name`, `shape`, `disk`, `preview_port` and
  `environment_commands_attributes` (a row with an `id` changes that command, or removes it with `_destroy`; one
  without adds it). The tier and size change only while no machine was made from it.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "onboarding_update",
    category: :workspace,
    summary: "Save onboarding's environment",
    patch: "/onboarding/environment",
    refuses: [404, 409, 422],
    access: :mutation,
    default_role: :manage

  param :disk, :integer

  param :environment_commands_attributes, {:list, :map} do
    param :id, :string
    param :kind, :string
    param :name, :string
    param :command, :string
    param :_destroy, :boolean
  end

  param :preview_port, :integer
  param :provider_name, :string
  param :repo_id, :string
  param :shape, :string
  alias Nvoi.Tools.Utils.Onboarding.Base

  def call(params, current) do
    Base.onboarding!(current)
    Base.reached!("environment", current)
    repo = Base.repo(params, current)
    attrs = Map.take(params, ~w(provider_name shape disk preview_port))

    environment =
      Nvoi.Onboarding.configure!(repo, attrs, rows(params["environment_commands_attributes"]))

    Nvoi.Live.signal(current.workspace.id, "onboarding")
    Base.state(Map.put(params, "repo_id", environment.repo_id), current)
  end

  # A list, or a form's map keyed by index.
  defp rows(nil), do: []
  defp rows(rows) when is_list(rows), do: Enum.filter(rows, &is_map/1)

  defp rows(rows) when is_map(rows),
    do:
      rows
      |> Enum.sort_by(fn {k, _} -> String.to_integer(to_string(k)) end)
      |> Enum.map(&elem(&1, 1))
end
