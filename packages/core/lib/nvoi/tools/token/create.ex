defmodule Nvoi.Tools.Token.Create do
  @moduledoc """
  A personal token for the caller, pinned to one of their workspaces (`workspace`, a slug; the current one when
  absent), named, lasting `expires_in_days`. Its secret is in this answer only. Only a person's own sign-in makes
  one: a personal token or a connected client making tokens would outlive its expiry and reach every workspace of
  its person.
  """
  use Nvoi.Tools.Action,
    group: "token",
    action: "create",
    category: :account,
    summary: "Make a personal token; its secret is shown once",
    post: "/tokens",
    status: 201,
    refuses: [403, 422],
    access: :sensitive,
    default_role: :open

  param :name, :string, required: true

  param :workspace, :string,
    doc: "a slug of one of the caller's workspaces; the current one when absent"

  param :expires_in_days, :integer, doc: "1 to 365; 90 when absent"

  # A personal token's life when the person names none, and the longest they may name.
  @default_days 90
  @max_days 365

  def call(params, current) do
    Nvoi.Tools.Action.person!(current)
    name = params["name"] |> to_string() |> String.trim()
    if name == "", do: refuse!("name can't be blank", "invalid_name", 422)

    days = days(params["expires_in_days"])
    workspace = workspace(params["workspace"], current)

    session =
      Postgres.insert!(%Nvoi.Session{
        kind: "personal",
        name: String.slice(name, 0, 100),
        user_id: current.user.id,
        workspace_id: workspace.id,
        user_agent: current.user_agent,
        ip_address: current.ip_address,
        expires_at: DateTime.add(DateTime.utc_now(), days * 24 * 3600)
      })

    session = %{session | workspace: workspace}

    {:created,
     %{
       token:
         session
         |> NvoiWeb.TokenResource.one(current.session && current.session.id)
         |> Map.put(:secret, Nvoi.Session.token(session))
     }}
  end

  defp days(nil), do: @default_days
  defp days(""), do: @default_days

  defp days(value) do
    case Integer.parse(to_string(value)) do
      {n, ""} when n in 1..@max_days -> n
      _ -> refuse!("expires_in_days must be 1 to #{@max_days}", "invalid_expires_in_days", 422)
    end
  end

  defp workspace(slug, current) when slug in [nil, ""],
    do:
      current.workspace ||
        refuse!("This token names no workspace: name one with workspace.", "not_a_member", 403)

  defp workspace(slug, current) do
    Postgres.one(where(Nvoi.Workspace.for_user(current.user), slug: ^to_string(slug))) ||
      refuse!(
        "#{slug} is not one of your workspaces: whoami lists the ones you are in.",
        "not_a_member",
        403
      )
  end
end
