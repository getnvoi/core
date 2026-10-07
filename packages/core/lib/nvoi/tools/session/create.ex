defmodule Nvoi.Tools.Session.Create do
  @moduledoc """
  `workspace` is a slug the user belongs to; absent, their first. The token is pinned to it. `mode` is the
  server's mode (`NvoiWeb.mode/0`): the CLI keeps it with the context and hides what the server does not serve.
  """
  use Nvoi.Tools.Action,
    group: "session",
    action: "create",
    category: :account,
    summary: "Log in and mint a token",
    post: "/login",
    status: 201,
    refuses: [401, 403],
    access: :sensitive,
    default_role: :open,
    # Answers before anyone is signed in.
    skip_authentication: true

  param :email, :string, required: true
  param :password, :string, required: true
  param :workspace, :string
  alias NvoiWeb.{UserResource, WorkspaceResource}

  def call(params, _current) do
    user =
      Nvoi.User.authenticate_by(to_string(params["email"]), to_string(params["password"])) ||
        refuse!("That email or password is incorrect.", "invalid_credentials", 401)

    slug = params["workspace"]
    query = Nvoi.Workspace.for_user(user)

    workspace =
      if slug in [nil, ""],
        do: Postgres.one(limit(query, 1)),
        else: Postgres.one(where(query, slug: ^slug))

    cond do
      workspace ->
        :ok

      slug in [nil, ""] ->
        refuse!(
          "This account is in no workspace: ask an admin to invite you, or sign up to open one.",
          "not_a_member",
          403
        )

      true ->
        refuse!(
          "#{slug} is not one of this account's workspaces: log in without a workspace, and whoami lists the ones you are in.",
          "not_a_member",
          403
        )
    end

    {:sign_in, user, workspace,
     %{
       user: UserResource.wrap(user),
       workspace: WorkspaceResource.wrap(workspace),
       mode: NvoiWeb.mode()
     }}
  end
end
