defmodule Nvoi.Tools.Session.Signup do
  @moduledoc """
  A new account from an email and a password, with its first workspace. Like every new account it waits on approval
  (`Nvoi.User.pending?/1`): no token until nvoi approves it by hand; then the person logs in. An address that has an
  account already is refused; it logs in instead.
  """
  use Nvoi.Tools.Action,
    group: "session",
    action: "signup",
    category: :account,
    summary:
      "Sign up with an email and a password: an account and its workspace, pending approval",
    post: "/signup",
    status: 201,
    refuses: [422],
    access: :sensitive,
    default_role: :open,
    # Answers before anyone is signed in.
    skip_authentication: true

  param :email, :string, required: true
  param :password, :string, required: true
  param :workspace, :string, required: true
  alias NvoiWeb.{UserResource, WorkspaceResource}

  # The shortest password accepted.
  @min_password 12

  def call(params, _current) do
    email = params["email"] |> to_string() |> String.trim() |> String.downcase()
    password = to_string(params["password"])

    if not String.match?(email, ~r/^[^@\s]+@[^@\s]+\.[^@\s]+$/),
      do: refuse!("#{email} is not an email address.", "invalid_email", 422)

    if String.length(password) < @min_password,
      do: refuse!("A password has at least #{@min_password} characters.", "weak_password", 422)

    if Postgres.exists?(from u in Nvoi.User, where: u.email == ^email),
      do: refuse!("#{email} has an account already: log in.", "account_exists", 422)

    {:ok, {user, workspace}} =
      Postgres.transaction(fn ->
        user =
          Postgres.insert!(%Nvoi.User{
            email: email,
            password_digest: Bcrypt.hash_pwd_salt(password)
          })

        {user, Nvoi.Workspace.open!(params["workspace"], user)}
      end)

    {:created, %{user: UserResource.wrap(user), workspace: WorkspaceResource.wrap(workspace)}}
  end
end
