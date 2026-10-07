defmodule Nvoi.Tools.Token.List do
  @moduledoc """
  The caller's own sign-ins that have not expired, most recently used first; never a secret. A personal token or a
  connected client sees those of its own workspace only, so it learns no other workspace of its person.
  """
  use Nvoi.Tools.Action,
    group: "token",
    action: "list",
    category: :account,
    summary: "List your tokens: app sign-ins, personal tokens and connected clients",
    get: "/tokens",
    access: :sensitive,
    reads: true,
    default_role: :open

  def call(_params, current) do
    now = DateTime.utc_now()

    sessions =
      Postgres.all(
        from s in Nvoi.Session,
          where: s.user_id == ^current.user.id,
          where: is_nil(s.expires_at) or s.expires_at > ^now,
          order_by: [desc_nulls_last: s.last_used_at, desc: s.created_at],
          preload: [:workspace]
      )
      |> Enum.filter(&Nvoi.Tools.Token.Delete.reaches?(current, &1))
      |> Enum.reject(&(DateTime.compare(Nvoi.Session.expires_at(&1), now) != :gt))

    %{tokens: Enum.map(sessions, &NvoiWeb.TokenResource.one(&1, current.session.id))}
  end
end
