defmodule Nvoi.Tools.Utils.Onboarding.Show do
  @moduledoc """
  The step, the repository it is about (`repo_id`, else the last imported) with its environment once saved, the
  agents, and whether a model credential exists; without one, `start` takes the default provider's fields in
  `values`.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "onboarding_show",
    category: :workspace,
    summary: "The onboarding step and what it holds",
    get: "/onboarding",
    refuses: [404],
    access: :free,
    default_role: :view

  param :repo_id, :string, in: :query

  def call(params, current), do: Nvoi.Tools.Utils.Onboarding.Base.state(params, current)
end
