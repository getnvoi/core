defmodule NvoiWeb.Hooks.TasksController do
  @moduledoc """
  A task's webhook: POST /hooks/tasks/:id with the task's bearer token starts a run. A json body's `text`, or any
  other body as sent, is appended to the prompt as data. No session: the token is the key.
  """
  use NvoiWeb, :controller
  alias NvoiWeb.ApiController

  # Calls per task and hour, counted whatever their token.
  @limit 20

  plug :limit

  def create(conn, %{"id" => id} = params) do
    case Nvoi.Task.authenticate(id, NvoiWeb.Authentication.bearer(conn)) do
      nil ->
        ApiController.error(conn, "No task with that id and token.", "unauthorized", 401)

      task ->
        case Nvoi.Task.fire!(task, "webhook", text: payload(conn, params)) do
          nil ->
            ApiController.error(
              conn,
              Nvoi.Postgres.get!(Nvoi.Task, task.id).last_error,
              "not_started",
              422
            )

          session ->
            conn
            |> put_status(201)
            |> json(%{
              session_id: session.id,
              session_url: "#{NvoiWeb.Authentication.base_url(conn)}/sessions/#{session.id}"
            })
        end
    end
  end

  defp payload(conn, params) do
    json = match?(["application/json" <> _], get_req_header(conn, "content-type"))

    cond do
      json and Map.has_key?(params, "text") -> to_string(params["text"])
      conn.assigns[:raw_body] -> conn.assigns.raw_body
      true -> with {:ok, body, _} <- read_body(conn), do: body, else: (_ -> "")
    end
  end

  # Counted per task and hour in the table the application made; older hours are dropped.
  defp limit(conn, _) do
    hour = div(System.system_time(:second), 3600)
    :ets.select_delete(__MODULE__, [{{{:_, :"$1"}, :_}, [{:<, :"$1", hour}], [true]}])
    count = :ets.update_counter(__MODULE__, {conn.params["id"], hour}, 1, {:key, 0})

    if count > @limit,
      do:
        conn
        |> ApiController.error("Too many runs this hour.", "too_many_requests", 429)
        |> halt(),
      else: conn
  end
end
