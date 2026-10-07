defmodule Nvoi.Access do
  @moduledoc """
  What one person may do in a workspace, and where. A role grants groups of actions; reach is decided on the
  environment, open to everyone or restricted to the members admitted to it and the admins. A record out of reach
  is not found.
  """
  import Ecto.Query
  alias Nvoi.{Environment, Postgres, WorkspaceUser}

  @groups %{
    "view" => ~w(editor manager admin),
    "drive" => ~w(editor manager admin),
    "approve" => ~w(manager admin),
    "manage" => ~w(manager admin),
    "own" => ~w(admin),
    # nvoi's staff (Nvoi.SystemUser), whatever their role in a workspace: no workspace role holds it.
    "system" => []
  }
  @rank ~w(editor manager admin)

  defstruct [:member, :workspace]

  defmodule Denied do
    defexception [:group]
    def message(%{group: nil}), do: "Your role does not allow this."
    def message(%{group: group}), do: "Your role does not allow this (#{group})."
    def code(%{group: "own"}), do: "not_an_owner"
    def code(_), do: "not_allowed"
  end

  def roles(group), do: Map.fetch!(@groups, group)
  def rank, do: @rank

  @doc "The groups, lowest first."
  def groups, do: ~w(view drive approve manage own)

  def for(user, workspace) do
    %__MODULE__{
      member: Postgres.get_by(WorkspaceUser.live(), user_id: user.id, workspace_id: workspace.id),
      workspace: workspace
    }
  end

  def role(%{member: nil}), do: nil
  def role(%{member: member}), do: to_string(member.role)
  def admin?(access), do: role(access) == "admin"
  def can?(access, group), do: role(access) in Map.fetch!(@groups, to_string(group))

  def check!(access, group) do
    can?(access, group) || raise Denied, group: to_string(group)
    :ok
  end

  def controlled(%{member: nil}), do: from(m in WorkspaceUser, where: false)

  def controlled(%{workspace: workspace} = access) do
    lower = Enum.take(@rank, Enum.find_index(@rank, &(&1 == role(access))))
    from m in WorkspaceUser.live(), where: m.workspace_id == ^workspace.id and m.role in ^lower
  end

  def narrowed?(%{member: nil}), do: false

  def narrowed?(%{member: member, workspace: workspace}) do
    to_string(member.role) != "admin" and Nvoi.Workspace.restricted_environments?(workspace)
  end

  @doc "The live environments in reach: what lists show and what is acted on."
  def environments(access), do: from(e in reach(access), where: is_nil(e.archived_at))

  @doc "Every environment in reach, archived ones included: their sessions, checkouts and artifacts stay to read."
  def reach(%{member: nil}), do: from(e in Environment, where: false)

  def reach(%{member: member, workspace: workspace} = access) do
    all =
      from e in Environment, join: r in assoc(e, :repo), where: r.workspace_id == ^workspace.id

    if narrowed?(access) do
      granted =
        from g in Nvoi.Grant, where: g.workspace_user_id == ^member.id, select: g.environment_id

      from e in all, where: not e.restricted or e.id in subquery(granted)
    else
      all
    end
  end

  @doc """
  A workflow narrowed to an environment follows it, archived or not: archiving the environment leaves the workflow
  to its owner. One with none is the workspace's.
  """
  def workflows(%{workspace: workspace} = access) do
    reach = from e in reach(access), select: e.id

    from w in Nvoi.Workflow,
      where:
        w.workspace_id == ^workspace.id and
          (is_nil(w.environment_id) or w.environment_id in subquery(reach))
  end

  def find_environment!(access, id),
    do: Postgres.one!(from e in environments(access), where: e.id == ^id)

  def tasks(%{workspace: workspace} = access) do
    reach = from e in environments(access), select: e.id

    from t in Nvoi.Task,
      where: t.workspace_id == ^workspace.id and t.environment_id in subquery(reach)
  end

  @doc "The repositories with an environment in reach, and those with none yet."
  def repos(%{member: nil}), do: from(r in Nvoi.Repo, where: false)

  def repos(%{workspace: workspace} = access) do
    all = from r in Nvoi.Repo.live(), where: r.workspace_id == ^workspace.id

    if narrowed?(access) do
      reach = from e in environments(access), select: e.repo_id
      any = from e in Environment.live(), select: e.repo_id
      from r in all, where: r.id in subquery(reach) or r.id not in subquery(any)
    else
      all
    end
  end

  def worktrees(access) do
    reach = from e in reach(access), select: e.id
    from w in Nvoi.Worktree, where: w.environment_id in subquery(reach)
  end

  @doc "Sessions on the environments in reach."
  def sessions(access) do
    reach = from w in worktrees(access), select: w.id

    from s in Nvoi.AgentSession,
      where: s.worktree_id in subquery(reach)
  end

  def artifacts(%{workspace: workspace} = access) do
    reach = from w in worktrees(access), select: w.id

    from a in Nvoi.Artifact,
      where:
        a.workspace_id == ^workspace.id and
          (is_nil(a.worktree_id) or a.worktree_id in subquery(reach))
  end
end
