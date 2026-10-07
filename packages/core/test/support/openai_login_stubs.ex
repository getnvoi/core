defmodule Nvoi.OpenaiLoginStubs do
  @moduledoc """
  OpenAI's device login answered in tests (`Req.Test` under `Llm.Login`): the code request, the wait (a sequence of
  answers, the last one repeated) and the exchange. Each request is sent to the test as `{:openai, path, body}`.
  """
  @api "/api/accounts"

  def grant,
    do: %{authorization_code: "authz-1", code_challenge: "chal-1", code_verifier: "verif-1"}

  @doc "An id token with the claims nvoi reads: the address and the ChatGPT account."
  def jwt do
    claims = %{
      "email" => "person@example.com",
      "https://api.openai.com/auth" => %{"chatgpt_account_id" => "acct-1"}
    }

    Enum.map_join(
      [%{"alg" => "none"}, claims],
      ".",
      &Base.url_encode64(Jason.encode!(&1), padding: false)
    ) <> ".sig"
  end

  def tokens, do: %{id_token: jwt(), access_token: "access-1", refresh_token: "refresh-1"}

  def usercode, do: {200, %{device_auth_id: "dev-1", user_code: "ABCD-12345", interval: "5"}}

  @doc """
  Answers per path: `usercode`, `wait` (a list), `exchange`; each a `{status, body}` or `:timeout`. Unset paths
  answer the defaults (a code, not yet, the tokens).
  """
  def stub_login(answers \\ []) do
    test = self()
    {:ok, waits} = Agent.start_link(fn -> answers[:wait] || [{403, %{}}] end)

    Req.Test.stub(Llm.Login, fn conn ->
      {:ok, raw, conn} = Plug.Conn.read_body(conn)

      send(
        test,
        {:openai, conn.request_path, raw, Plug.Conn.get_req_header(conn, "content-type")}
      )

      answer =
        case conn.request_path do
          @api <> "/deviceauth/usercode" ->
            answers[:usercode] || usercode()

          @api <> "/deviceauth/token" ->
            Agent.get_and_update(waits, fn
              [last] -> {last, [last]}
              [next | rest] -> {next, rest}
            end)

          "/oauth/token" ->
            answers[:exchange] || {200, tokens()}
        end

      case answer do
        :timeout ->
          Req.Test.transport_error(conn, :timeout)

        {status, body} ->
          conn |> Plug.Conn.put_status(status) |> Req.Test.json(body)
      end
    end)
  end
end
