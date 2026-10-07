defmodule NvoiWeb.CloudCredentialResource do
  use NvoiWeb.Resource

  def one(credential) do
    %{
      id: credential.id,
      provider: credential.provider,
      token: Nvoi.CloudCredential.masked(credential),
      checked_at: credential.checked_at,
      created_at: credential.created_at
    }
  end
end
