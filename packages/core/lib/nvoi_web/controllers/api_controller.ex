defmodule NvoiWeb.ApiController do
  @moduledoc """
  Runs the route's tool and renders what it answers. Refusals take one of two shapes:
  `422 {"errors": {"name": ["can't be blank"]}}` for a field, `4xx {"error": sentence, "code": snake_case}` otherwise.
  """
  use Phoenix.Controller, formats: [:json]
  import Plug.Conn
  alias Nvoi.Tools.Action

  def run(conn, params) do
    tool = conn.private.tool

    if Action.ported?(tool) do
      conn |> answer(tool, Action.run(tool, params, conn.assigns.current))
    else
      error(conn, "#{inspect(tool)} is not ported yet.", "not_ported", 501)
    end
  rescue
    e ->
      case refusal(e) do
        {status, body} -> conn |> put_status(status) |> json(body)
        nil -> reraise e, __STACKTRACE__
      end
  end

  @doc "What the API answers for an error an action raised, as {status, body}; nil for one it does not answer."
  def refusal(%Action.Refused{} = e), do: {e.status, %{error: e.message, code: e.code}}

  # Said the same whether the record is in another workspace or nowhere: neither is revealed. An id that is not an id
  # at all is nowhere too.
  def refusal(%e{}) when e in [Ecto.NoResultsError, Ecto.Query.CastError],
    do:
      {404,
       %{
         error:
           "That does not exist in this token's workspace, or your role cannot see it. If it is in another " <>
             "workspace, use a context of that workspace (--context).",
         code: "not_found"
       }}

  def refusal(%Ecto.InvalidChangesetError{} = e), do: {422, %{errors: errors(e.changeset)}}

  def refusal(%Nvoi.User.Pending{} = e), do: {403, %{error: e.message, code: "pending_approval"}}

  def refusal(%Nvoi.Billing.Refused{} = e),
    do: {402, %{error: e.message, code: "payment_required"}}

  def refusal(%Nvoi.AgentSession.Unrunnable{} = e),
    do: {422, %{error: e.message, code: "no_model_credential"}}

  def refusal(%Nvoi.Environment.Production{} = e),
    do: {422, %{error: e.message, code: "production"}}

  def refusal(%Compute.Error{} = e),
    do:
      {502,
       %{
         error:
           "The machine or its provider did not answer: #{String.slice(e.message, 0, 200)}. Try again in a moment.",
         code: "unavailable"
       }}

  # Two writes of one record raced, and the other won.
  def refusal(%Ecto.ConstraintError{}),
    do: {409, %{error: "It changed meanwhile. Read it again and retry.", code: "conflict"}}

  def refusal(_), do: nil

  # The sign-in and its token together: an account pending approval gets neither (Nvoi.Session.token/1 refuses).
  defp answer(conn, _, {:sign_in, user, workspace, body}) do
    {:ok, token} =
      Nvoi.Postgres.transaction(fn ->
        %Nvoi.Session{
          user_id: user.id,
          workspace_id: workspace.id,
          user_agent: conn.assigns.current.user_agent,
          ip_address: conn.assigns.current.ip_address
        }
        |> Nvoi.Postgres.insert!()
        |> Nvoi.Session.token()
      end)

    render_json(conn, 201, Map.merge(%{token: token}, body))
  end

  defp answer(conn, Nvoi.Tools.Session.Delete, nil) do
    Nvoi.Session.revoke!(conn.assigns.current.session)
    send_resp(conn, 204, "")
  end

  defp answer(conn, _, nil), do: send_resp(conn, 204, "")

  defp answer(conn, _, {:file, bytes, type, filename}),
    do: send_download(conn, {:binary, bytes}, filename: filename, content_type: type)

  defp answer(conn, _, {:created, nil}), do: send_resp(conn, 201, "")
  defp answer(conn, _, {:accepted, nil}), do: send_resp(conn, 202, "")
  defp answer(conn, _, {:created, body}), do: render_json(conn, 201, body)
  defp answer(conn, _, {:accepted, body}), do: render_json(conn, 202, body)
  defp answer(conn, _, {:not_found, body}), do: render_json(conn, 404, body)
  defp answer(conn, _, {:payment_required, body}), do: render_json(conn, 402, body)
  defp answer(conn, _, body), do: render_json(conn, 200, body)

  defp render_json(conn, status, body) do
    body = plain(body)
    NvoiWeb.Contract.check(conn, status, body)
    conn |> put_status(status) |> json(body)
  end

  @doc "Times in JSON as the API document has them: UTC, milliseconds, `Z`."
  def plain(%DateTime{} = time),
    do: time |> DateTime.truncate(:millisecond) |> DateTime.to_iso8601()

  def plain(%Date{} = date), do: Date.to_iso8601(date)
  def plain(%Decimal{} = decimal), do: Decimal.to_string(decimal)
  def plain(%_{} = struct), do: struct
  def plain(map) when is_map(map), do: Map.new(map, fn {k, v} -> {k, plain(v)} end)
  def plain(list) when is_list(list), do: Enum.map(list, &plain/1)
  def plain(value), do: value

  @doc "Answers a refusal outside a tool: a webhook or a callback (Nvoi.GitHub.off/0)."
  def refused(conn, %Action.Refused{} = e), do: error(conn, e.message, e.code, e.status)

  def error(conn, message, code, status),
    do: conn |> put_status(status) |> json(%{error: message, code: code})

  def errors(changeset) do
    Ecto.Changeset.traverse_errors(changeset, fn {message, opts} ->
      # Only the keys the message names: an option may be a list (unsafe_validate_unique's fields).
      Enum.reduce(opts, message, fn {key, value}, acc ->
        String.replace(acc, "%{#{key}}", fn _ -> to_string(value) end)
      end)
    end)
  end
end
