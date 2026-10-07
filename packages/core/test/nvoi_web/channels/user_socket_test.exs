defmodule NvoiWeb.UserSocketTest do
  use Nvoi.DataCase, async: false
  import Phoenix.ChannelTest
  alias NvoiWeb.{Presence, SessionChannel, UserSocket, WorkspaceChannel}

  @endpoint NvoiWeb.Endpoint

  setup do
    workspace =
      Postgres.insert!(%Nvoi.Workspace{
        name: "Ben",
        slug: "ben-#{System.unique_integer([:positive])}"
      })

    admin = member(workspace, "admin", "Ada", "Lovelace")
    editor = member(workspace, "editor", "Eve", "Smith")
    repo = Postgres.insert!(%Nvoi.Repo{name: "ben/dummy", workspace_id: workspace.id})
    %{workspace: workspace, admin: admin, editor: editor, repo: repo}
  end

  defp member(workspace, role, first, last) do
    user =
      Postgres.insert!(%Nvoi.User{
        pending_approval: false,
        email: "#{String.downcase(first)}-#{System.unique_integer([:positive])}@nvoi.test",
        first_name: first,
        last_name: last,
        password_digest: "x"
      })

    Postgres.insert!(%Nvoi.WorkspaceUser{
      user_id: user.id,
      workspace_id: workspace.id,
      role: role
    })

    sign_in = Postgres.insert!(%Nvoi.Session{user_id: user.id, workspace_id: workspace.id})
    %{user: user, sign_in: sign_in, token: Nvoi.Session.token(sign_in)}
  end

  defp session_on(repo, restricted) do
    environment =
      Postgres.insert!(%Nvoi.Environment{
        repo_id: repo.id,
        name: "env-#{System.unique_integer([:positive])}",
        branch: "main",
        disk: 10,
        provider_name: "daytona",
        restricted: restricted
      })

    worktree =
      Postgres.insert!(%Nvoi.Worktree{
        repo_id: repo.id,
        environment_id: environment.id,
        base_ref: "main",
        branch: "w-#{System.unique_integer([:positive])}",
        slug: "w-#{System.unique_integer([:positive])}",
        provider_name: "daytona",
        provider_type: "vm"
      })

    Postgres.insert!(%Nvoi.AgentSession{worktree_id: worktree.id})
  end

  test "a socket is signed in by the API token, and refused without one", %{admin: admin} do
    assert {:ok, socket} = connect(UserSocket, %{"token" => admin.token})
    assert socket.assigns.user.id == admin.user.id
    assert UserSocket.id(socket) == "user_socket:#{admin.sign_in.id}"
    assert connect(UserSocket, %{"token" => "nope"}) == {:error, :unauthorized}
    assert connect(UserSocket, %{}) == {:error, :unauthorized}
  end

  test "a token whose sign-in is gone, or whose member left, is refused", %{
    workspace: workspace,
    editor: editor
  } do
    Postgres.delete_all(from m in Nvoi.WorkspaceUser, where: m.user_id == ^editor.user.id)
    assert connect(UserSocket, %{"token" => editor.token}) == {:error, :unauthorized}
    Postgres.delete!(editor.sign_in)
    assert connect(UserSocket, %{"token" => editor.token}) == {:error, :unauthorized}
    assert workspace
  end

  test "the workspace channel relays Nvoi.Live, only on the token's workspace", %{
    workspace: workspace,
    admin: admin
  } do
    {:ok, socket} = connect(UserSocket, %{"token" => admin.token})

    assert {:error, %{reason: "unauthorized"}} =
             subscribe_and_join(socket, WorkspaceChannel, "workspace:#{Ecto.UUID.generate()}")

    {:ok, _, _} = subscribe_and_join(socket, WorkspaceChannel, "workspace:#{workspace.id}")

    Nvoi.Live.signal(workspace.id, "inbox")
    assert_push "change", %{type: "inbox"}
  end

  test "while an environment is restricted, a session out of a member's reach is not named to them",
       %{workspace: workspace, admin: admin, editor: editor, repo: repo} do
    hidden = session_on(repo, true)
    {:ok, socket} = connect(UserSocket, %{"token" => editor.token})
    {:ok, _, _} = subscribe_and_join(socket, WorkspaceChannel, "workspace:#{workspace.id}")
    {:ok, admin_socket} = connect(UserSocket, %{"token" => admin.token})
    {:ok, _, _} = subscribe_and_join(admin_socket, WorkspaceChannel, "workspace:#{workspace.id}")

    Nvoi.Live.signal(workspace.id, "sessions", hidden.id)
    assert_push "change", %{type: "sessions", session_id: id}
    assert id == hidden.id
    assert_push "change", message
    assert message == %{type: "sessions"}
    refute_push "change", _
  end

  test "session presence: who has it open, joins and leaves; out of reach is refused",
       %{admin: admin, editor: editor, repo: repo} do
    open = session_on(repo, false)
    hidden = session_on(repo, true)

    {:ok, editor_socket} = connect(UserSocket, %{"token" => editor.token})

    assert {:error, %{reason: "not_found"}} =
             subscribe_and_join(editor_socket, SessionChannel, "session:#{hidden.id}")

    {:ok, _, editor_channel} =
      subscribe_and_join(editor_socket, SessionChannel, "session:#{open.id}")

    assert_push "presence_state", state
    assert [%{metas: [%{name: "Eve Smith", initials: "ES"}]}] = Map.values(state)

    {:ok, admin_socket} = connect(UserSocket, %{"token" => admin.token})

    {:ok, _, admin_channel} =
      subscribe_and_join(admin_socket, SessionChannel, "session:#{open.id}")

    admin_id = admin.user.id
    assert_push "presence_diff", %{joins: %{^admin_id => %{metas: [%{name: "Ada Lovelace"}]}}}

    assert Presence.list("session:#{open.id}") |> Map.keys() |> Enum.sort() ==
             Enum.sort([admin.user.id, editor.user.id])

    Process.unlink(admin_channel.channel_pid)
    leave(admin_channel)
    assert_push "presence_diff", %{leaves: %{^admin_id => _}}
    assert Map.keys(Presence.list("session:#{open.id}")) == [editor.user.id]
    assert editor_channel
  end

  test "signing out closes the sign-in's sockets", %{admin: admin} do
    {:ok, socket} = connect(UserSocket, %{"token" => admin.token})
    @endpoint.subscribe(UserSocket.id(socket))
    UserSocket.disconnect(admin.sign_in.id)
    assert_receive %Phoenix.Socket.Broadcast{event: "disconnect"}
  end
end
