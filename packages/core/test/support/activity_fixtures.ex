defmodule Nvoi.ActivityFixtures do
  @moduledoc "A workspace, its members signed in, and the activity rows the calls left, for the activity tests."
  import Ecto.Query
  alias Nvoi.Postgres

  def workspace(name \\ "W"),
    do:
      Postgres.insert!(%Nvoi.Workspace{
        name: name,
        slug: "w#{System.unique_integer([:positive])}"
      })

  def member(workspace, role, email \\ nil) do
    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: email || "#{role}#{System.unique_integer([:positive])}@nvoi.test",
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: role
    })

    user
  end

  @doc "The conn signed in as the user in the workspace."
  def signed_in(conn, user, workspace) do
    session = Postgres.insert!(%Nvoi.Session{user_id: user.id, workspace_id: workspace.id})
    Plug.Conn.put_req_header(conn, "authorization", "Bearer " <> Nvoi.Session.token(session))
  end

  @doc "Writes what the buffer holds, then every row, oldest first."
  def rows do
    Nvoi.Activity.Buffer.flush()
    Postgres.all(from a in Nvoi.Activity, order_by: [a.created_at, a.id])
  end

  @doc "Drops what other tests left waiting in the buffer."
  def drain do
    Nvoi.Activity.Buffer.flush()
    Postgres.delete_all(Nvoi.Activity)
  end
end
