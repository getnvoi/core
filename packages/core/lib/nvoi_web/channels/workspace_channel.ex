defmodule NvoiWeb.WorkspaceChannel do
  @moduledoc """
  Everything the clients show changes through here: `workspace:<id>`, the token's own workspace, pushes `change`
  with `Nvoi.Live`'s message (`type`, the API path that changed, and `session_id` when one session did). While some
  environment is restricted, the channel of a member who is not an admin checks, in its own process, that the
  session is in their reach; a session elsewhere is not named to them.
  """
  use Phoenix.Channel
  import Ecto.Query, only: [from: 2]
  alias Nvoi.{Access, Postgres, Workspace, WorkspaceUser}

  @impl true
  def join(
        "workspace:" <> id,
        _,
        %{assigns: %{workspace: %{id: id}, user: user}} = socket
      ) do
    member = Postgres.get_by(WorkspaceUser.live(), user_id: user.id, workspace_id: id)

    if member && NvoiWeb.UserSocket.remaining(socket) > 0 do
      Phoenix.PubSub.subscribe(Nvoi.PubSub, Nvoi.Live.topic(id))
      Phoenix.PubSub.subscribe(Nvoi.PubSub, Nvoi.Live.everyone())
      Process.send_after(self(), :expired, NvoiWeb.UserSocket.remaining(socket) * 1000)
      {:ok, assign(socket, member: member)}
    else
      {:error, %{reason: "unauthorized"}}
    end
  end

  def join(_, _, _), do: {:error, %{reason: "unauthorized"}}

  @impl true
  def handle_info({:live, message}, socket) do
    push(socket, "change", visible(message, socket.assigns))
    {:noreply, socket}
  end

  def handle_info(:expired, socket), do: {:stop, :normal, socket}

  defp visible(%{session_id: id} = message, %{member: member, workspace: workspace}) do
    access = %Access{member: member, workspace: workspace}

    if Access.admin?(access) or not Workspace.restricted_environments?(workspace) or
         Postgres.exists?(from s in Access.sessions(access), where: s.id == ^id),
       do: message,
       else: Map.delete(message, :session_id)
  end

  defp visible(message, _), do: message
end
