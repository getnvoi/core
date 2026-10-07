defmodule NvoiWeb.EdgeController do
  @moduledoc """
  What the preview worker asks, and nothing else. It holds no provider key: it asks where to go, gets a host and a
  set of headers, and sets them verbatim, so a provider change reaches it as a different answer. Three routes on the
  sandbox host and nowhere else; the gate token (`x-nvoi-token`, or a bearer) is all they authenticate with.

    GET  /route   where this preview lives right now
    POST /boot    where it stands; with start=1, bring it back and answer before it has
    POST /touch   somebody is looking at it

  A terminal token asks /route and /touch about the machine's shell server instead; it never boots anything. A
  preview token may name an artifact rather than a worktree: its route is a port on the worktree's box, and any
  document request boots it, since a visitor has no Start to press.
  """
  use NvoiWeb, :controller
  import Ecto.Query
  alias Nvoi.{Gate, Postgres, Preview, Worktree}

  # Per worktree, per minute, well over what the worker asks: they bound a loop rather than shape traffic.
  @budgets %{"route" => 120, "touch" => 60, "boot" => 120}
  @terminal_port 7777

  plug :previews
  plug :authenticate
  plug :budget

  # An installation without the worker has nothing to answer it (Nvoi.Previews).
  defp previews(conn, _) do
    if Nvoi.Previews.enabled?(),
      do: conn,
      else:
        conn
        |> put_status(501)
        |> json(%{error: Nvoi.Previews.message(), code: "previews_not_set_up"})
        |> halt()
  end

  # `gone` and `unavailable` are not the same answer: a worktree nobody has is 410, which the worker renders as "no
  # preview here"; a box that is not up is 503, which asks the worker for a boot.
  def route(conn, _) do
    gate = conn.assigns.gate
    if artifact?(gate), do: artifact_route(conn, gate), else: worktree_route(conn, gate)
  end

  defp worktree_route(conn, gate) do
    with %Worktree{} = worktree <- live_worktree(gate) do
      cond do
        gate.kind != Gate.terminal() and gate.port == @terminal_port ->
          send_resp(conn, 403, "")

        true ->
          reachable =
            if gate.kind == Gate.terminal(),
              do: Nvoi.Terminal.route(worktree),
              else: Preview.route(worktree, gate.port)

          if reachable == :unavailable, do: send_resp(conn, 503, ""), else: json(conn, reachable)
      end
    else
      _ -> send_resp(conn, 410, "")
    end
  rescue
    Compute.Error -> send_resp(conn, 503, "")
  end

  # 202 before it is up: the browser holds a wait page, and the work outlives the request. `start=1` is a person's
  # press; without it nothing starts.
  def boot(conn, params) do
    gate = conn.assigns.gate

    cond do
      gate.kind == Gate.terminal() ->
        send_resp(conn, 403, "")

      artifact?(gate) ->
        case live_artifact(gate) do
          nil ->
            send_resp(conn, 410, "")

          # Any document starts a stopped app; a failed one starts again only on the press, so the wait page's
          # refresh does not rebuild a broken version every five seconds.
          artifact ->
            start = params["start"] == "1" or artifact.state != "failed"
            conn |> put_status(202) |> json(Nvoi.Artifact.boot(artifact, start))
        end

      worktree = live_worktree(gate) ->
        conn |> put_status(202) |> json(Preview.boot(worktree, params["start"] == "1"))

      true ->
        send_resp(conn, 410, "")
    end
  rescue
    e in Nvoi.Billing.Refused ->
      conn
      |> put_status(202)
      |> json(%{
        state: "failed",
        steps: [],
        failure: %{process: "box", exit_code: nil, cause: nil, message: e.message}
      })
  end

  # The idle clock: a preview being looked at is not parked under the viewer.
  def touch(conn, _) do
    gate = conn.assigns.gate
    record = if artifact?(gate), do: live_artifact(gate), else: live_worktree(gate)

    case record do
      nil ->
        send_resp(conn, 410, "")

      # An app being looked at keeps the box it runs on awake.
      %Nvoi.Artifact{} = artifact ->
        Postgres.update_all(from(a in Nvoi.Artifact, where: a.id == ^artifact.id),
          set: [used_at: DateTime.utc_now()]
        )

        Worktree.used(artifact.worktree_id)
        send_resp(conn, 204, "")

      worktree ->
        Worktree.used(worktree.id)
        send_resp(conn, 204, "")
    end
  end

  defp artifact_route(conn, gate) do
    case live_artifact(gate) do
      nil ->
        send_resp(conn, 410, "")

      artifact ->
        case Nvoi.Artifact.route(artifact) do
          :unavailable -> send_resp(conn, 503, "")
          reachable -> json(conn, reachable)
        end
    end
  rescue
    Compute.Error -> send_resp(conn, 503, "")
  end

  # Whether the preview token names an artifact rather than a worktree. Both ids are UUIDs.
  defp artifact?(gate) do
    gate.kind != Gate.terminal() and match?({:ok, _}, Ecto.UUID.cast(gate.box)) and
      Postgres.exists?(from a in Nvoi.Artifact, where: a.id == ^gate.box)
  end

  # An artifact that is live and has a version; archived or never saved is gone.
  defp live_artifact(gate) do
    Postgres.one(
      from a in Nvoi.Artifact,
        as: :a,
        where:
          a.id == ^gate.box and is_nil(a.archived_at) and
            exists(from v in Nvoi.ArtifactVersion, where: v.artifact_id == parent_as(:a).id)
    )
  end

  # Looked up on every call rather than trusted from the token: it stays valid for its two hours after the worktree
  # is gone, its environment archived or without its preview. A terminal needs no preview.
  defp live_worktree(gate) do
    with {:ok, _} <- Ecto.UUID.cast(gate.box),
         %Worktree{} = worktree <- Postgres.get(Worktree, gate.box),
         %Nvoi.Environment{archived_at: nil} = environment <-
           Postgres.get!(Nvoi.Environment, worktree.environment_id),
         true <- gate.kind == Gate.terminal() or Nvoi.Environment.preview?(environment) do
      worktree
    else
      _ -> nil
    end
  end

  defp authenticate(conn, _) do
    token =
      List.first(get_req_header(conn, "x-nvoi-token")) ||
        with(
          "Bearer " <> t <- List.first(get_req_header(conn, "authorization")) || "",
          do: t,
          else: (_ -> nil)
        )

    claims = Gate.verify(token, [Gate.audience(), Gate.terminal()])
    kind = claims["aud"]
    port = claims["port"]

    if not (is_binary(claims["box"]) and is_binary(claims["host"]) and
              (kind == Gate.terminal() or is_integer(port))),
       do: raise(Gate.Invalid, message: "claims")

    assign(conn, :gate, %{
      box: claims["box"],
      host: claims["host"],
      port: if(kind != Gate.terminal(), do: port),
      kind: kind
    })
  rescue
    Gate.Invalid -> conn |> put_status(401) |> json(%{error: "unauthorized"}) |> halt()
  end

  # Counted per worktree, action and minute in the table the application made; older minutes are dropped.
  defp budget(conn, _) do
    action = conn |> action_name() |> to_string()
    minute = div(System.system_time(:second), 60)
    :ets.select_delete(__MODULE__, [{{{:_, :_, :"$1"}, :_}, [{:<, :"$1", minute}], [true]}])
    count = :ets.update_counter(__MODULE__, {conn.assigns.gate.box, action, minute}, 1, {:key, 0})

    if count > Map.fetch!(@budgets, action),
      do:
        conn
        |> put_resp_header("retry-after", "60")
        |> put_status(429)
        |> json(%{error: "too many requests"})
        |> halt(),
      else: conn
  end
end
