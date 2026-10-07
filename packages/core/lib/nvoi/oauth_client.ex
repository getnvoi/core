defmodule Nvoi.OauthClient do
  @moduledoc """
  A client that registered itself for OAuth (RFC 7591), such as Claude's connector or another MCP client. It is public:
  it holds no secret, and every authorization it asks for proves itself with PKCE.
  """
  use Nvoi.Schema

  schema "oauth_clients" do
    field :name, :string
    field :redirect_uris, {:array, :string}
    timestamps()
  end

  # The most redirect addresses one client registers.
  @max_redirects 10

  def changeset(client, attrs) do
    client
    |> cast(attrs, [:name, :redirect_uris])
    |> update_change(:name, &String.slice(String.trim(&1), 0, 100))
    |> validate_required([:name, :redirect_uris])
    |> validate_length(:redirect_uris, min: 1, max: @max_redirects)
    |> validate_change(:redirect_uris, fn _, uris ->
      if Enum.all?(uris, &redirect?/1),
        do: [],
        else: [redirect_uris: "must be https, a loopback http address or an app's own scheme"]
    end)
  end

  @doc "An address a code may be sent to: https, http on the loopback, or an app's own scheme; never a fragment."
  def redirect?(uri) when is_binary(uri) do
    case URI.parse(uri) do
      %URI{fragment: nil, scheme: "https", host: host} when host not in [nil, ""] ->
        true

      %URI{fragment: nil, scheme: "http", host: host} ->
        host in ~w(localhost 127.0.0.1 ::1 [::1])

      %URI{fragment: nil, scheme: scheme}
      when scheme not in [nil, "javascript", "data", "file"] ->
        true

      _ ->
        false
    end
  end

  def redirect?(_), do: false
end
