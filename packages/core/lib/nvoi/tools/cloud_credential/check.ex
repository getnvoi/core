defmodule Nvoi.Tools.CloudCredential.Check do
  @moduledoc "A read on the provider with the kept token: answered with the time it last worked, or refused with why."
  use Nvoi.Tools.Action,
    group: "cloud_credential",
    action: "check",
    category: :cloud,
    summary: "Check a cloud credential with its provider",
    post: "/cloud/credentials/{id}/check",
    refuses: [404, 422],
    access: :sensitive,
    reads: true,
    production: true,
    default_role: :manage

  param :id, :string, in: :path
  alias Nvoi.Tools.CloudCredential.Base

  def call(params, current) do
    credential = Base.credential(params, current)

    case Nvoi.CloudCredential.check(credential.provider, credential.token) do
      :ok ->
        Base.answer(
          Postgres.update!(Ecto.Changeset.change(credential, checked_at: DateTime.utc_now()))
        )

      {:error, reason} ->
        refuse!("#{credential.provider} refused the token: #{reason}", "check_failed", 422)
    end
  end
end
