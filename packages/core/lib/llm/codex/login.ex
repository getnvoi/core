defmodule Llm.Codex.Login do
  @moduledoc """
  The device-code login of OpenAI's Codex CLI (openai/codex, codex-rs/login/src/device_code_auth.rs and
  server.rs), run by nvoi so a person connects a ChatGPT subscription with a code and a browser and never handles
  a file. nvoi gets its own grant: it does not share the person's CLI login or its refresh token.

      start     POST {API}/deviceauth/usercode {client_id}              -> device_auth_id, user_code, interval
      poll      POST {API}/deviceauth/token {device_auth_id, user_code} -> 403/404 not yet; 200 the grant
      exchange  POST {BASE}/oauth/token, a form: grant_type=authorization_code, code, redirect_uri, client_id,
                code_verifier                                           -> id_token, access_token, refresh_token

  `credentials` renders the tokens as the auth.json the CLI reads on the box, so nothing downstream changes.
  """
  alias Llm.Login
  alias Llm.Login.{Code, Error, Grant, Tokens}

  @base "https://auth.openai.com"
  @api "#{@base}/api/accounts"
  @client_id "app_EMoamEEZ73f0CkXaXp7hrann"
  @verification_url "#{@base}/codex/device"
  @redirect_uri "#{@base}/deviceauth/callback"
  # How long a code lives, from the CLI's own limit.
  @lifetime 15 * 60
  # The wait between two looks when OpenAI names none.
  @interval 5
  @disabled "Device login is not enabled for this ChatGPT account. In ChatGPT, open Settings, " <>
              "then Security, and turn on device code login for Codex (a workspace admin may need " <>
              "to allow it), then connect again."

  def verification_url, do: @verification_url
  def client_id, do: @client_id

  def start(now \\ DateTime.utc_now()) do
    case Login.post("#{@api}/deviceauth/usercode", {:json, %{client_id: @client_id}}) do
      {200, body} ->
        id = to_string(body["device_auth_id"])
        code = to_string(body["user_code"] || body["usercode"])

        if id == "" or code == "",
          do: raise(Error.new(:refused, "OpenAI answered the code request without a code."))

        %Code{
          device_auth_id: id,
          user_code: code,
          interval: interval(body["interval"]),
          expires_at: expiry(body["expires_at"], now),
          verification_url: @verification_url
        }

      {404, _} ->
        raise Error.new(:disabled, @disabled)

      {status, _} ->
        raise Error.new(:refused, "OpenAI refused the code request (status #{status}).")
    end
  end

  @doc "nil while the person has not approved, a Grant when they did."
  def poll(device_auth_id, user_code) do
    case Login.post(
           "#{@api}/deviceauth/token",
           {:json, %{device_auth_id: device_auth_id, user_code: user_code}}
         ) do
      {status, _} when status in [403, 404] ->
        nil

      {200, body} ->
        grant = %Grant{
          authorization_code: to_string(body["authorization_code"]),
          code_challenge: to_string(body["code_challenge"]),
          code_verifier: to_string(body["code_verifier"])
        }

        if Login.incomplete?(grant),
          do: raise(Error.new(:refused, "OpenAI approved the sign-in without a grant.")),
          else: grant

      {status, body} ->
        if Jason.encode!(body) =~ ~r/denied/i,
          do: raise(Error.new(:denied, "The sign-in was denied in the browser.")),
          else:
            raise(Error.new(:refused, "OpenAI answered the sign-in check with status #{status}."))
    end
  end

  def exchange(%Grant{} = grant) do
    form = %{
      grant_type: "authorization_code",
      code: grant.authorization_code,
      redirect_uri: @redirect_uri,
      client_id: @client_id,
      code_verifier: grant.code_verifier
    }

    case Login.post("#{@base}/oauth/token", {:form, form}) do
      {200, body} ->
        tokens = %Tokens{
          id_token: to_string(body["id_token"]),
          access_token: to_string(body["access_token"]),
          refresh_token: to_string(body["refresh_token"])
        }

        if Login.incomplete?(tokens),
          do:
            raise(Error.new(:refused, "OpenAI answered the exchange without all three tokens.")),
          else: tokens

      {status, body} ->
        why = body["error_description"] || body["error"]
        why = if is_binary(why), do: ": #{why}", else: ""
        raise Error.new(:refused, "OpenAI refused the exchange (status #{status}#{why}).")
    end
  end

  @doc """
  `{settings, secrets}` to merge into the credential. The secret is the auth.json the CLI reads; the account (an
  address, shown to the person) is read from the id token.
  """
  def credentials(%Tokens{} = tokens, now \\ DateTime.utc_now()) do
    claims = claims(tokens.id_token)

    auth =
      if is_map(claims["https://api.openai.com/auth"]),
        do: claims["https://api.openai.com/auth"],
        else: %{}

    file =
      Jason.OrderedObject.new([
        {"auth_mode", "chatgpt"},
        {"OPENAI_API_KEY", nil},
        {"tokens",
         Jason.OrderedObject.new(
           [
             {"id_token", tokens.id_token},
             {"access_token", tokens.access_token},
             {"refresh_token", tokens.refresh_token},
             {"account_id", auth["chatgpt_account_id"]}
           ]
           |> Enum.reject(fn {_, v} -> is_nil(v) end)
         )},
        {"last_refresh", now |> DateTime.truncate(:microsecond) |> DateTime.to_iso8601()}
      ])

    settings =
      if is_binary(claims["email"]),
        do: %{"kind" => "chatgpt", "account" => claims["email"]},
        else: %{"kind" => "chatgpt"}

    {settings, %{"token" => Jason.encode!(file)}}
  end

  defp interval(value) do
    case Integer.parse(String.trim(to_string(value))) do
      {seconds, ""} when seconds > 0 -> seconds
      _ -> @interval
    end
  end

  # The earlier of the vendor's own expiry and the CLI's limit.
  defp expiry(value, now) do
    limit = DateTime.add(now, @lifetime)

    case DateTime.from_iso8601(to_string(value)) do
      {:ok, given, _} -> if DateTime.compare(given, limit) == :lt, do: given, else: limit
      _ -> limit
    end
  end

  # A JWT's claims, unverified: read for display and an id, never trusted.
  defp claims(jwt) do
    with [_, payload | _] <- String.split(to_string(jwt), "."),
         {:ok, json} <- Base.url_decode64(payload, padding: false),
         {:ok, %{} = claims} <- Jason.decode(json) do
      claims
    else
      _ -> %{}
    end
  end
end
