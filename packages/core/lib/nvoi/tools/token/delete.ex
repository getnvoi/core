defmodule Nvoi.Tools.Token.Delete do
  @moduledoc """
  Revokes one of the caller's own sign-ins and closes its sockets; another person's is not found. A personal token
  or a connected client reaches those of its own workspace only.
  """
  use Nvoi.Tools.Action,
    group: "token",
    action: "delete",
    category: :account,
    summary: "Revoke one of your tokens",
    delete: "/tokens/{id}",
    status: 204,
    refuses: [404],
    access: :sensitive,
    default_role: :open

  param :id, :uuid, in: :path

  def call(params, current) do
    session =
      with {:ok, id} <- Ecto.UUID.cast(params["id"] || ""),
           %Nvoi.Session{} = session <-
             Postgres.get_by(Nvoi.Session, id: id, user_id: current.user.id),
           true <- reaches?(current, session) do
        session
      else
        _ -> refuse!("You have no token with that id.", "not_found", 404)
      end

    Nvoi.Session.revoke!(session)
    nil
  end

  @doc "Whether the caller sees and revokes this sign-in of theirs: every one from their own, else its workspace's."
  def reaches?(current, session),
    do:
      Nvoi.Session.decides?(current.session) or
        session.workspace_id == current.session.workspace_id
end
