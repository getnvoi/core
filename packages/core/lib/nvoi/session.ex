defmodule Nvoi.Session do
  @moduledoc """
  A sign-in, which a token names: `app` (the desktop, the CLI's login), `personal` (made and named by a member) or
  `oauth` (a client connected through OAuth, named after it). Deleting the row revokes every token naming it.
  """
  use Nvoi.Schema

  @kinds ~w(app personal oauth)

  schema "sessions" do
    field :ip_address, :string
    field :user_agent, :string
    field :kind, :string, default: "app"
    field :name, :string
    field :expires_at, :utc_datetime_usec
    field :last_used_at, :utc_datetime_usec
    field :refresh_digest, :binary
    belongs_to :user, Nvoi.User
    belongs_to :workspace, Nvoi.Workspace
    belongs_to :oauth_client, Nvoi.OauthClient
    timestamps()
  end

  def kinds, do: @kinds

  @doc """
  Whether this sign-in is a person's own (the app or the CLI, kind `app`), the only kind that decides: approves a
  held call or an approval-level action, denies one, answers an agent's question. A personal token (a script, an
  MCP client set up by `nvoi mcp add`) and an OAuth client act for the member but never decide. Nil, an internal
  caller with no sign-in, is not a token and is not refused here.
  """
  def decides?(nil), do: true
  def decides?(%{kind: kind}), do: kind == "app"

  # How long an app's sign-in lasts when the row says no expiry.
  @lifetime 30 * 24 * 3600
  # last_used_at is written at most once in this many seconds per sign-in.
  @used_every 60

  @doc "When the sign-in ends: its own expiry, else an app token's lifetime from when it was made."
  def expires_at(%{expires_at: %DateTime{} = at}), do: at
  def expires_at(%{created_at: %DateTime{} = at}), do: DateTime.add(at, @lifetime)
  def expires_at(_), do: DateTime.add(DateTime.utc_now(), @lifetime)

  def expired?(%{expires_at: %DateTime{} = at}),
    do: DateTime.compare(at, DateTime.utc_now()) != :gt

  def expired?(_), do: false

  @doc "Records that the sign-in was used, at most once a minute."
  def used(session) do
    now = DateTime.utc_now()

    if session.last_used_at == nil or DateTime.diff(now, session.last_used_at) >= @used_every do
      Nvoi.Postgres.update_all(from(s in __MODULE__, where: s.id == ^session.id),
        set: [last_used_at: now]
      )
    end

    :ok
  end

  @doc "Deletes the sign-in and closes its sockets: every token naming it stops working."
  def revoke!(session) do
    Nvoi.Postgres.delete!(session)
    NvoiWeb.UserSocket.disconnect(session.id)
  end

  @doc """
  The bearer for this sign-in: its id and its expiry, HS256 with a key derived from secret_key_base. The one place a
  token is made: an account pending approval gets none (`Nvoi.User.Pending`).
  """
  def token(session) do
    if Nvoi.User.pending?(Nvoi.Postgres.get(Nvoi.User, session.user_id)),
      do: raise(Nvoi.User.Pending)

    exp =
      if session.expires_at,
        do: DateTime.to_unix(session.expires_at),
        else: System.os_time(:second) + @lifetime

    claims = Jason.encode!(%{sid: session.id, exp: exp})
    signed = b64(~s({"alg":"HS256"})) <> "." <> b64(claims)
    signed <> "." <> b64(:crypto.mac(:hmac, :sha256, key(), signed))
  end

  @doc """
  The sign-in a token names, with its user and workspace: the one place every door reads a token. `{:ok, session}`;
  `:pending` for an account pending approval; `:error` when the token is absent, malformed, expired, signed with
  another key, or names no sign-in.
  """
  def resolve(token) do
    with id when is_binary(id) <- id_from_token(token),
         %__MODULE__{} = session <- Nvoi.Postgres.get(__MODULE__, id),
         false <- expired?(session),
         session = Nvoi.Postgres.preload(session, [:user, :workspace]) do
      if Nvoi.User.pending?(session.user), do: :pending, else: {:ok, session}
    else
      _ -> :error
    end
  end

  @doc "The id a token names, or nil when it is absent, malformed, expired or signed with another key."
  def id_from_token(token), do: claims_from_token(token)["sid"]

  @doc "A token's claims (`sid`, `exp`), or an empty map when it is absent, malformed, expired or signed with another key."
  def claims_from_token(token) when is_binary(token) do
    with [header, claims, signature] <- String.split(token, "."),
         {:ok, signature} <- Base.url_decode64(signature, padding: false),
         true <-
           :crypto.hash_equals(
             signature,
             :crypto.mac(:hmac, :sha256, key(), header <> "." <> claims)
           ),
         {:ok, json} <- Base.url_decode64(claims, padding: false),
         {:ok, %{"sid" => _, "exp" => exp} = claims} <- Jason.decode(json),
         true <- exp > System.os_time(:second) do
      claims
    else
      _ -> %{}
    end
  end

  def claims_from_token(_), do: %{}

  defp b64(data), do: Base.url_encode64(data, padding: false)

  defp key,
    do: :crypto.mac(:hmac, :sha256, Nvoi.Credentials.fetch!(["secret_key_base"]), "session token")
end
