defmodule Nvoi.Tools.Environment.Update do
  @moduledoc """
  A rename, the preview's port, or what checkouts run on. The latter is refused while checkouts exist (code
  `checkouts`); with purge=true they are deleted in the background and the change saved after them (202).
  `restricted` and `member_ids` say who reaches it; a person sets only the members they control. `kind` makes it a
  production environment (no agent, no checkout, deployments only) or a development one again while it has no
  deployments; `deploy` says whether a ship deploys a production environment at once (`auto`) or waits for a
  person (`approval`).
  """
  use Nvoi.Tools.Action,
    group: "environment",
    action: "update",
    category: :environment,
    summary: "Rename an environment, set its kind, its preview or its database",
    patch: "/environments/{id}",
    put: true,
    also: [202],
    refuses: [404, 422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path
  param :kind, :string, values: Nvoi.Environment.kinds(), doc: "development or production"

  param :deploy, :string,
    values: Nvoi.Environment.deploys(),
    doc: "What a ship into a production environment's branch does: approval (by default) or auto"

  param :images_kept, :integer,
    doc: "Ready images a production environment keeps; older ones are pruned. 10 by default"

  param :branch, :string
  param :database_engine, :string
  param :database_version, :string
  param :disk, :integer
  param :member_ids, {:list, :string}
  param :name, :string
  param :preview_port, :string
  param :provider_name, :string
  param :purge, :boolean
  param :restricted, :boolean
  param :shape, :string
  alias Nvoi.Environment
  alias Nvoi.Tools.Environment.Base

  @reshaping ~w(provider_name shape disk branch database_engine database_version)

  def call(params, current) do
    environment = Base.environment(params, current)

    environment =
      if Nvoi.Text.blank?(params["kind"]),
        do: environment,
        else: Environment.kind!(environment, to_string(params["kind"]))

    environment =
      if Nvoi.Text.blank?(params["images_kept"]),
        do: environment,
        else:
          Postgres.update!(
            Ecto.Changeset.change(environment)
            |> Ecto.Changeset.cast(%{images_kept: params["images_kept"]}, [:images_kept])
            |> Ecto.Changeset.validate_number(:images_kept, greater_than_or_equal_to: 1)
          )

    environment =
      if Nvoi.Text.blank?(params["deploy"]),
        do: environment,
        else:
          Postgres.update!(
            Ecto.Changeset.change(environment, deploy: to_string(params["deploy"]))
          )

    attrs =
      if Map.has_key?(params, "name"),
        do: %{"name" => String.trim(to_string(params["name"]))},
        else: %{}

    attrs =
      Enum.reduce(@reshaping ++ ["preview_port"], attrs, fn k, acc ->
        if Map.has_key?(params, k),
          do: Map.put(acc, k, Nvoi.Text.presence(String.trim(to_string(params[k])))),
          else: acc
      end)

    purge = params["purge"] in [true, "true", "1"]
    changeset = Environment.changeset(environment, attrs)

    {environment, purging} =
      case Keyword.get_values(changeset.errors, :base) do
        [{message, [kind: :checkouts]} | _] when not purge ->
          refuse!(message, "checkouts", 422)

        [{_, [kind: :checkouts]} | _] when length(changeset.errors) == 1 ->
          later = Map.take(changeset.changes, Enum.map(@reshaping, &String.to_atom/1))
          now = Environment.save!(Environment.changeset(environment, Map.drop(attrs, @reshaping)))
          Oban.insert!(Nvoi.Jobs.PurgeJob.new(%{id: environment.id, changes: later}))
          {now, true}

        _ ->
          {Environment.save!(changeset), false}
      end

    admit(environment, params, current)
    answer = Base.answer(environment)
    if purging, do: {:accepted, answer}, else: answer
  end

  defp admit(environment, params, current) do
    if Map.has_key?(params, "restricted") or Map.has_key?(params, "member_ids") do
      access = current.access

      restricted =
        if Map.has_key?(params, "restricted"),
          do: params["restricted"] in [true, "true", "1"],
          else: environment.restricted

      ids = admitted(environment, params, access)

      granted = fn ->
        Postgres.all(
          from g in Nvoi.Grant.live(),
            where: g.environment_id == ^environment.id,
            select: g.workspace_user_id
        )
      end

      ids =
        if restricted and not Access.admin?(access),
          do: Enum.uniq((ids || granted.()) ++ [access.member.id]),
          else: ids

      Environment.admit!(environment, restricted, ids)
    end
  end

  # Theirs among those they control, with the admitted members they do not control.
  defp admitted(environment, %{"member_ids" => sent}, access) do
    sent = sent |> List.wrap() |> Enum.reject(&Nvoi.Text.blank?/1) |> Enum.uniq()

    known =
      Postgres.all(
        from m in Nvoi.WorkspaceUser.live(),
          where: m.workspace_id == ^access.workspace.id and m.id in ^sent,
          select: m.id
      )

    controlled = from m in Access.controlled(access), select: m.id
    mine = Postgres.all(from m in Access.controlled(access), where: m.id in ^sent, select: m.id)

    kept =
      Postgres.all(
        from g in Nvoi.Grant.live(),
          where:
            g.environment_id == ^environment.id and
              g.workspace_user_id not in subquery(controlled),
          select: g.workspace_user_id
      )

    Enum.uniq(kept ++ mine ++ (sent -- known))
  end

  defp admitted(_, _, _), do: nil
end
