defmodule Llm.CodexLoginTest do
  # The device login against OpenAI's answers, each stubbed: what a person is told when each goes wrong.
  use ExUnit.Case, async: true
  import Nvoi.OpenaiLoginStubs
  alias Llm.Codex.Login
  alias Llm.Login.{Error, Grant, Tokens}

  @api "/api/accounts"

  test "the provider offers the login for the chatgpt kind and no secret field for it" do
    assert Llm.Provider.login(Llm.Codex) == Login
    assert Llm.Codex.connect_kinds() == ["chatgpt"]
    assert Llm.Provider.login(Llm.ClaudeCode) == nil
    token = Enum.find(Llm.Codex.fields(), &(&1.key == "token"))
    assert token.kinds == ["api_key"]
  end

  test "a code request sends the Codex client id and answers the code, the page and the wait" do
    stub_login()
    code = Login.start()
    assert_received {:openai, @api <> "/deviceauth/usercode", body, ["application/json" <> _]}
    assert Jason.decode!(body) == %{"client_id" => "app_EMoamEEZ73f0CkXaXp7hrann"}
    assert {code.device_auth_id, code.user_code, code.interval} == {"dev-1", "ABCD-12345", 5}
    assert code.verification_url == "https://auth.openai.com/codex/device"
    assert_in_delta DateTime.diff(code.expires_at, DateTime.utc_now()), 15 * 60, 5
  end

  test "an interval sent as a string, a code under its other name and a far expiry are read" do
    far = DateTime.utc_now() |> DateTime.add(7200) |> DateTime.to_iso8601()

    stub_login(
      usercode: {200, %{device_auth_id: "d", usercode: "WXYZ", interval: " 7 ", expires_at: far}}
    )

    code = Login.start()
    assert {code.user_code, code.interval} == {"WXYZ", 7}
    assert_in_delta DateTime.diff(code.expires_at, DateTime.utc_now()), 15 * 60, 5
  end

  test "a code request refused with 404 says the device login is off and where to turn it on" do
    stub_login(usercode: {404, %{}})
    error = assert_raise Error, fn -> Login.start() end
    assert error.kind == :disabled
    assert error.message =~ "Settings, then Security"
  end

  test "a code request that fails otherwise, or answers no code, is refused with the status" do
    stub_login(usercode: {500, %{}})
    error = assert_raise Error, fn -> Login.start() end
    assert error.kind == :refused and error.message =~ "status 500"
    stub_login(usercode: {200, %{device_auth_id: "d"}})
    assert assert_raise(Error, fn -> Login.start() end).kind == :refused
  end

  test "OpenAI not answering is unreachable" do
    stub_login(usercode: :timeout)
    error = assert_raise Error, fn -> Login.start() end
    assert error.kind == :unreachable
    assert error.message =~ "auth.openai.com did not answer"
  end

  test "the wait is nothing on 403 and on 404, then the grant on 200" do
    stub_login(wait: [{403, %{}}, {404, %{}}, {200, grant()}])
    assert Login.poll("dev-1", "ABCD-12345") == nil
    assert Login.poll("dev-1", "ABCD-12345") == nil
    assert Login.poll("dev-1", "ABCD-12345") == struct(Grant, grant())
    assert_received {:openai, @api <> "/deviceauth/token", body, _}
    assert Jason.decode!(body) == %{"device_auth_id" => "dev-1", "user_code" => "ABCD-12345"}
  end

  test "a denied sign-in, another status and a grant with a part missing end the wait" do
    stub_login(wait: [{400, %{error: "access_denied"}}])
    assert assert_raise(Error, fn -> Login.poll("d", "u") end).kind == :denied
    stub_login(wait: [{500, %{}}])
    error = assert_raise Error, fn -> Login.poll("d", "u") end
    assert error.kind == :refused and error.message =~ "status 500"
    stub_login(wait: [{200, Map.delete(grant(), :code_verifier)}])
    assert assert_raise(Error, fn -> Login.poll("d", "u") end).kind == :refused
  end

  test "the exchange is a form with the grant, the redirect and the client id" do
    stub_login()
    tokens = Login.exchange(struct(Grant, grant()))
    assert_received {:openai, "/oauth/token", body, ["application/x-www-form-urlencoded" <> _]}

    assert URI.decode_query(body) == %{
             "grant_type" => "authorization_code",
             "code" => "authz-1",
             "redirect_uri" => "https://auth.openai.com/deviceauth/callback",
             "client_id" => "app_EMoamEEZ73f0CkXaXp7hrann",
             "code_verifier" => "verif-1"
           }

    assert {tokens.access_token, tokens.refresh_token} == {"access-1", "refresh-1"}
  end

  test "an exchange OpenAI refuses says its reason; one without all the tokens is refused" do
    stub_login(exchange: {400, %{error: "invalid_grant", error_description: "Code already used"}})
    error = assert_raise Error, fn -> Login.exchange(struct(Grant, grant())) end
    assert error.kind == :refused and error.message =~ "status 400: Code already used"
    stub_login(exchange: {200, %{id_token: jwt(), access_token: "a"}})
    assert assert_raise(Error, fn -> Login.exchange(struct(Grant, grant())) end).kind == :refused
  end

  test "the tokens are rendered as the auth.json the CLI reads, the account from the id token" do
    {settings, secrets} =
      Login.credentials(struct(Tokens, tokens()), ~U[2026-10-04 12:00:00.000000Z])

    assert settings == %{"kind" => "chatgpt", "account" => "person@example.com"}
    file = Jason.decode!(secrets["token"], objects: :ordered_objects)
    assert Enum.map(file.values, &elem(&1, 0)) == ~w(auth_mode OPENAI_API_KEY tokens last_refresh)
    assert file["auth_mode"] == "chatgpt" and file["OPENAI_API_KEY"] == nil

    assert Enum.map(file["tokens"].values, &elem(&1, 0)) ==
             ~w(id_token access_token refresh_token account_id)

    assert file["tokens"]["account_id"] == "acct-1"
    assert file["last_refresh"] == "2026-10-04T12:00:00.000000Z"
    assert Llm.Codex.problems(settings, secrets) == []
    assert Map.has_key?(Llm.Codex.files(settings, secrets), "auth.json")
  end
end
