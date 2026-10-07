defmodule Nvoi.Tools.GitHubSignIn.Collect do
  @moduledoc """
  Collects a sign-in with its key: `pending` while the person is on GitHub, `refused` with why, `code` once GitHub
  sent them back and the code their browser was handed is still to be given, or, once, with the right code, a new
  token with the user and their workspace. Asked again after that, or after the state's age, it is gone.
  """
  use Nvoi.Tools.Action,
    group: "github_sign_in",
    action: "collect",
    category: :account,
    summary: "Collect a GitHub sign-in: pending, refused, or a token once",
    post: "/github/sign_ins/{id}/collect",
    status: 200,
    also: [201],
    refuses: [404, 410, 422],
    access: :sensitive,
    default_role: :open,
    # Answers before anyone is signed in.
    skip_authentication: true

  param :id, :string, in: :path
  param :key, :string, required: true

  param :code, :string,
    doc:
      "the code GitHub's return handed the browser (nvoi://github?…&code=, or shown on the page)"

  alias Nvoi.GitHubSignIn

  def call(params, _current) do
    Nvoi.GitHub.enabled!()

    row =
      with {:ok, id} <- Ecto.UUID.cast(params["id"] || ""),
           %GitHubSignIn{} = row <- Postgres.get(GitHubSignIn, id),
           true <- GitHubSignIn.key?(row, params["key"]) do
        row
      else
        _ -> refuse!("No sign-in with that id and key.", "not_found", 404)
      end

    cond do
      row.collected_at != nil or GitHubSignIn.expired?(row) ->
        refuse!("That sign-in is over. Sign in with GitHub again.", "gone", 410)

      row.state == "pending" ->
        %{github_sign_in: %{id: row.id, state: "pending"}}

      row.state == "refused" ->
        %{github_sign_in: %{id: row.id, state: "refused", message: row.message}}

      Nvoi.Text.blank?(params["code"]) ->
        %{github_sign_in: %{id: row.id, state: "code"}}

      not GitHubSignIn.code?(row, params["code"]) ->
        row = GitHubSignIn.missed!(row)

        if row.state == "refused",
          do: %{github_sign_in: %{id: row.id, state: "refused", message: row.message}},
          else: refuse!("That code is not the one your browser shows.", "wrong_code", 422)

      GitHubSignIn.collect(row) ->
        user = Postgres.get!(Nvoi.User, row.user_id)
        workspace = Postgres.get!(Nvoi.Workspace, row.workspace_id)

        {:sign_in, user, workspace,
         %{
           github_sign_in: %{id: row.id, state: "signed_in"},
           user: NvoiWeb.UserResource.wrap(user),
           workspace: NvoiWeb.WorkspaceResource.wrap(workspace)
         }}

      true ->
        refuse!("That sign-in is over. Sign in with GitHub again.", "gone", 410)
    end
  end
end
