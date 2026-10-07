defmodule Nvoi.SystemUser do
  @moduledoc """
  nvoi's own staff: the system role, which reads activity across workspaces (funnels). No workspace member holds it;
  a row here gives it to a person, whatever their roles in workspaces.
  """
  use Ecto.Schema

  @primary_key {:user_id, :binary_id, autogenerate: false}

  schema "system_users" do
    field :created_at, :utc_datetime_usec, autogenerate: {DateTime, :utc_now, []}
  end

  def staff?(nil), do: false
  def staff?(user), do: Nvoi.Postgres.get(__MODULE__, user.id) != nil

  def grant!(user),
    do: Nvoi.Postgres.insert!(%__MODULE__{user_id: user.id}, on_conflict: :nothing)
end
