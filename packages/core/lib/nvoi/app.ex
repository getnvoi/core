defmodule Nvoi.App do
  @moduledoc """
  An app asked for: a name and a first message saying what to build. Started, it is a repository in nvoi's own git
  (`Nvoi.Repo.create_app!`) and a session whose first message is that prompt.
  """
  import Ecto.Changeset
  import Ecto.Query
  alias Nvoi.Postgres

  @doc """
  The session building it. A missing name or prompt, or a name another app holds, raises `Ecto.InvalidChangesetError`;
  `Nvoi.Forge.Error`, `Nvoi.Billing.Refused` and `Nvoi.AgentSession.Unrunnable` are the caller's to say.
  """
  def start!(workspace, user, name, prompt) do
    asked =
      {%{}, %{name: :string, prompt: :string}}
      |> cast(%{name: String.trim(to_string(name)), prompt: String.trim(to_string(prompt))}, [
        :name,
        :prompt
      ])
      |> validate_required(:name, message: "Name the app")
      |> validate_required(:prompt, message: "Say what it should do")
      |> apply_action!(:insert)

    agent =
      Postgres.one!(
        from a in Nvoi.Agent,
          where: a.workspace_id == ^workspace.id and is_nil(a.archived_at),
          order_by: a.created_at,
          limit: 1
      )

    repo = Nvoi.Repo.create_app!(workspace, asked.name)
    environment = Postgres.get_by!(Nvoi.Environment, repo_id: repo.id)
    Nvoi.AgentSession.start!(repo, environment, agent, asked.prompt, user)
  end
end
