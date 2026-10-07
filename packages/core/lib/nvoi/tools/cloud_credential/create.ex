defmodule Nvoi.Tools.CloudCredential.Create do
  @moduledoc """
  The workspace's key to its cloud account, checked with a read on the provider before it is kept. One per provider:
  a second replaces the first's token (a rotation), checked the same way.
  """
  use Nvoi.Tools.Action,
    group: "cloud_credential",
    action: "create",
    category: :cloud,
    summary: "Add the workspace's cloud credential",
    post: "/cloud/credentials",
    status: 201,
    refuses: [422],
    access: :sensitive,
    production: true,
    default_role: :manage

  param :provider, :string, required: true, doc: "hetzner"
  param :token, :string, required: true, doc: "The provider's API token, read and write"

  def call(params, current) do
    provider = params["provider"] |> to_string() |> String.trim()
    token = params["token"] |> to_string() |> String.trim()
    if token == "", do: refuse!("token is required", "parameter_missing", 422)

    case Nvoi.CloudCredential.put!(current.workspace.id, provider, token) do
      {:ok, credential} ->
        {:created, Nvoi.Tools.CloudCredential.Base.answer(credential)}

      {:error, reason} ->
        refuse!("#{provider} refused the token: #{reason}", "check_failed", 422)
    end
  end
end
