defmodule NvoiWeb.UserSocket do
  @moduledoc """
  The socket the clients hold at `/socket` (Phoenix's V2 JSON protocol), signed in by the API token in its connect
  params (`token`), which names a sign-in row: a channel knows who joined and the workspace the token is pinned to.
  A token pinned to a workspace stops working when its member leaves it. An MCP client's token is MCP's alone and is
  refused here, as on the API. Signing out closes the sign-in's sockets; a channel refuses to join, and leaves, once
  the token has expired.
  """
  use Phoenix.Socket
  alias Nvoi.{Postgres, Session}

  channel "workspace:*", NvoiWeb.WorkspaceChannel
  channel "session:*", NvoiWeb.SessionChannel

  @impl true
  def connect(%{"token" => token}, socket, connect_info) when is_binary(token) do
    # On the site's host alone in production (NvoiWeb.Hosts); Nvoi.Session.resolve/1 refuses an account pending
    # approval, as every door does; an MCP client's token is MCP's.
    with true <- NvoiWeb.Hosts.socket?(host(connect_info)),
         {:ok, session} <- Session.resolve(token),
         true <- session.kind != "oauth",
         true <- member?(session) do
      {:ok,
       assign(socket,
         session: session,
         user: session.user,
         workspace: session.workspace,
         expires_at: Session.claims_from_token(token)["exp"]
       )}
    else
      _ -> {:error, :unauthorized}
    end
  end

  def connect(_, _, _), do: {:error, :unauthorized}

  defp host(%{uri: %URI{host: host}}), do: host
  defp host(_), do: nil

  @impl true
  def id(socket), do: socket_id(socket.assigns.session.id)
  defp socket_id(session_id), do: "user_socket:#{session_id}"

  @doc "Closes every socket of a sign-in, when it is deleted."
  def disconnect(session_id),
    do: NvoiWeb.Endpoint.broadcast(socket_id(session_id), "disconnect", %{})

  @doc "Seconds until the socket's token expires; a channel leaves then."
  def remaining(socket), do: socket.assigns.expires_at - System.os_time(:second)

  defp member?(%{workspace_id: nil}), do: true

  defp member?(session),
    do:
      Postgres.get_by(Nvoi.WorkspaceUser.live(),
        user_id: session.user_id,
        workspace_id: session.workspace_id
      ) != nil
end
