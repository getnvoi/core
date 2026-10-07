defmodule Nvoi.GitHubApp do
  @moduledoc """
  The installation's GitHub App: `github_app` in the credentials, each key read as any secret (Nvoi.Credentials), so
  `GITHUB_APP_ID` sets `github_app.id`.
  """
  @keys ~w(id slug client_id client_secret webhook_secret private_key)

  @doc "The credential paths of the App's keys: GitHub is on when every one resolves (Nvoi.GitHub.enabled?/0)."
  def paths, do: for(key <- @keys, do: ["github_app", key])

  for key <- @keys -- ["private_key"] do
    def unquote(String.to_atom(key))(),
      do: to_string(Nvoi.Credentials.fetch!(["github_app", unquote(key)]))
  end

  @doc "The PEM, with its line breaks or on one line with them written `\\n` (a variable in an env file)."
  def private_key,
    do: String.replace(Nvoi.Credentials.fetch!(["github_app", "private_key"]), "\\n", "\n")

  @doc "A JWT for the App, valid nine minutes, RS256 with its private key."
  def jwt(now \\ System.os_time(:second)) do
    [key] = :public_key.pem_decode(private_key())
    key = :public_key.pem_entry_decode(key)
    b64 = &Base.url_encode64(&1, padding: false)

    signed =
      b64.(~s({"alg":"RS256","typ":"JWT"})) <>
        "." <> b64.(Jason.encode!(%{iat: now - 60, exp: now + 540, iss: id()}))

    signed <> "." <> b64.(:public_key.sign(signed, :sha256, key))
  end

  # A state outlives the few clicks on GitHub, no more: one kept in a log or a history goes nowhere after it.
  @state_age 600
  @salt "github state"
  @authorize "https://github.com/login/oauth/authorize?"

  def state_age, do: @state_age

  @doc "The path GitHub sends a person back to, as the App is configured with it."
  def callback(base_url), do: base_url <> "/oauth/github/callback"

  @doc """
  The state a journey carries to GitHub and back: its purpose (`install`, `sign_in`, or `connect` for an MCP
  client's authorization, whose signed request rides along), its row, the workspace and member for an install, and
  a nonce the row keeps the digest of, so the state is spent once.
  """
  def sign_state(purpose, id, nonce, workspace_id \\ nil, user_id \\ nil, request \\ nil),
    do:
      Phoenix.Token.sign(NvoiWeb.Endpoint, @salt, %{
        "p" => purpose,
        "id" => id,
        "n" => nonce,
        "w" => workspace_id,
        "u" => user_id,
        "r" => request
      })

  @doc "The state's claims, or :error when it is forged, edited or older than the state's age."
  def verify_state(state) when is_binary(state) do
    case Phoenix.Token.verify(NvoiWeb.Endpoint, @salt, state, max_age: @state_age) do
      {:ok, %{"p" => p, "id" => id, "n" => n} = claims}
      when p in ~w(install sign_in connect) and is_binary(id) and is_binary(n) ->
        {:ok, claims}

      _ ->
        :error
    end
  end

  def verify_state(_), do: :error

  @doc "A fresh nonce, and its digest for the row."
  def nonce do
    nonce = Base.url_encode64(:crypto.strong_rand_bytes(24), padding: false)
    {nonce, digest(nonce)}
  end

  def digest(value), do: :crypto.hash(:sha256, value)

  @doc "Whether a nonce matches the digest a row keeps, in constant time."
  def nonce?(nonce, digest) when is_binary(nonce) and is_binary(digest),
    do: Plug.Crypto.secure_compare(digest(nonce), digest)

  def nonce?(_, _), do: false

  @doc "The OAuth authorize URL: who the person is on GitHub. A GitHub App's takes no scope."
  def authorize_url(state, base_url),
    do:
      @authorize <>
        URI.encode_query(client_id: client_id(), redirect_uri: callback(base_url), state: state)

  @doc """
  Sign-in's authorize URL, with the PKCE challenge of the verifier the sign-in row keeps: a code caught on its way
  back is useless without it.
  """
  def sign_in_url(state, verifier, base_url) do
    challenge = Base.url_encode64(:crypto.hash(:sha256, verifier), padding: false)

    @authorize <>
      URI.encode_query(
        client_id: client_id(),
        redirect_uri: callback(base_url),
        state: state,
        code_challenge: challenge,
        code_challenge_method: "S256"
      )
  end

  @doc """
  The account picker: select_target always shows it, where installations/new skips it for somebody already
  installed, who is exactly the person adding a second organisation.
  """
  def install_url(state, base_url),
    do:
      "https://github.com/apps/#{slug()}/installations/select_target?" <>
        URI.encode_query(state: state, redirect_uri: callback(base_url))

  def signed?(body, signature) do
    expected =
      "sha256=" <>
        Base.encode16(:crypto.mac(:hmac, :sha256, webhook_secret(), body), case: :lower)

    Plug.Crypto.secure_compare(expected, to_string(signature))
  end
end
