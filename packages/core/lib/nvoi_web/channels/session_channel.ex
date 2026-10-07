defmodule NvoiWeb.SessionChannel do
  @moduledoc """
  A member with a session open: joining `session:<id>` tracks them in `NvoiWeb.Presence` under their user id, one
  entry per open page (`name`, `initials`, `joined_at`); leaving, or the socket going silent, drops the entry. The
  channel pushes `presence_state` on joining and `presence_diff` on every change. A token watches only sessions of
  the workspace it is pinned to, within the environments its member reaches.
  """
  use Phoenix.Channel
  import Ecto.Query, only: [from: 2]
  alias Nvoi.{Access, Postgres, User}
  alias NvoiWeb.Presence

  @impl true
  def join("session:" <> id, _, %{assigns: %{workspace: %{} = workspace, user: user}} = socket) do
    with {:ok, id} <- Ecto.UUID.cast(id),
         true <- NvoiWeb.UserSocket.remaining(socket) > 0,
         %{} <-
           Postgres.one(
             from s in Access.sessions(Access.for(user, workspace)), where: s.id == ^id
           ) do
      send(self(), :joined)
      Process.send_after(self(), :expired, NvoiWeb.UserSocket.remaining(socket) * 1000)
      {:ok, socket}
    else
      _ -> {:error, %{reason: "not_found"}}
    end
  end

  def join(_, _, _), do: {:error, %{reason: "unauthorized"}}

  @impl true
  def handle_info(:joined, %{assigns: %{user: user}} = socket) do
    {:ok, _} =
      Presence.track(socket, user.id, %{
        name: User.name(user),
        initials: User.initials(user),
        joined_at: DateTime.utc_now()
      })

    push(socket, "presence_state", Presence.list(socket))
    {:noreply, socket}
  end

  def handle_info(:expired, socket), do: {:stop, :normal, socket}
end
