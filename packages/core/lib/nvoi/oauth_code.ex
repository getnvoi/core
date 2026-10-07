defmodule Nvoi.OauthCode do
  @moduledoc """
  An authorization code: who allowed which client into which workspace, where the code may go back, and the PKCE
  challenge. Only its digest is kept, and exchanging it deletes the row, so a code works once.
  """
  use Nvoi.Schema

  schema "oauth_codes" do
    field :code_digest, :binary
    field :redirect_uri, :string
    field :challenge, :string
    field :expires_at, :utc_datetime_usec
    belongs_to :oauth_client, Nvoi.OauthClient
    belongs_to :user, Nvoi.User
    belongs_to :workspace, Nvoi.Workspace
    timestamps()
  end

  # A code is exchanged by the client the moment it is back, so it lives a minute.
  @lifetime 60

  @doc "A new code for this grant: the row and the code, given once."
  def issue!(client, user, workspace, redirect_uri, challenge) do
    code = Base.url_encode64(:crypto.strong_rand_bytes(32), padding: false)

    row =
      Nvoi.Postgres.insert!(%__MODULE__{
        code_digest: digest(code),
        oauth_client_id: client.id,
        user_id: user.id,
        workspace_id: workspace.id,
        redirect_uri: redirect_uri,
        challenge: challenge,
        expires_at: DateTime.add(DateTime.utc_now(), @lifetime)
      })

    {row, code}
  end

  @doc "Takes the code: its row, deleted in the same statement, or nil when it is unknown, spent or too old."
  def spend(code) when is_binary(code) do
    case Nvoi.Postgres.delete_all(
           from(c in __MODULE__, where: c.code_digest == ^digest(code), select: c)
         ) do
      {1, [row]} -> if DateTime.compare(row.expires_at, DateTime.utc_now()) == :gt, do: row
      _ -> nil
    end
  end

  def spend(_), do: nil

  @doc "Whether the verifier is the one the S256 challenge was made from."
  def verifier?(row, verifier) when is_binary(verifier) do
    computed = Base.url_encode64(:crypto.hash(:sha256, verifier), padding: false)
    Plug.Crypto.secure_compare(computed, row.challenge)
  end

  def verifier?(_, _), do: false

  def digest(value), do: :crypto.hash(:sha256, value)
end
