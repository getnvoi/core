defmodule Nvoi.Tools.Utils.Usage.Show do
  @moduledoc """
  Where the workspace's credit went (Usage): spend per day over `days` (7, 14, 30 or 90; 30 when absent or
  another number), by environment, repository and machine, beside the credit left and the machines running now.
  What the workspace spends is a manager's to read: an environment the reader does not reach is not named.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "usage_show",
    category: :billing,
    summary: "How much the machines spent over a month or another range of days",
    get: "/usage",
    refuses: [401, 403],
    access: :free,
    default_role: :manage

  param :days, :integer, in: :query

  def call(params, current) do
    workspace = current.workspace
    reach = from(e in Nvoi.Access.environments(current.access), select: e.id)

    hidden =
      Postgres.all(
        from e in Nvoi.Environment,
          join: r in assoc(e, :repo),
          where: r.workspace_id == ^workspace.id and e.id not in subquery(reach),
          select: e.id
      )

    Map.merge(
      %{usage: NvoiWeb.UsageResource.one(Nvoi.Usage.new(workspace, params["days"], hidden))},
      Nvoi.Billing.standing(workspace)
    )
  end
end
