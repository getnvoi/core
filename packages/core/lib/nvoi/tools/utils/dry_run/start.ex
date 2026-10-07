defmodule Nvoi.Tools.Utils.DryRun.Start do
  @moduledoc "A fresh checkout with a hidden session, its box starting in the background."
  use Nvoi.Tools.Action,
    group: "utils",
    action: "dry_run_start",
    category: :environment,
    summary: "Start a dry run",
    post: "/environments/{id}/dry_run",
    status: 202,
    refuses: [422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :id, :string, in: :path

  def call(params, current) do
    environment = Nvoi.Tools.Environment.Base.environment(params, current)

    if not Nvoi.Environment.preview?(environment),
      do: refuse!("Add a preview command first.", "no_preview_command", 422)

    agent =
      Postgres.one(
        from a in Nvoi.Agent,
          where: a.workspace_id == ^current.workspace.id,
          order_by: a.created_at,
          limit: 1
      )

    session = Nvoi.Environment.dry_run!(environment, agent && agent.id)
    {:accepted, %{session: NvoiWeb.SessionResource.wrap(session)}}
  rescue
    e in Nvoi.Billing.Refused -> refuse!(e.message, "payment_required", 402)
  end
end
