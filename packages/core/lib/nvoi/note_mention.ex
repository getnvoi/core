defmodule Nvoi.NoteMention do
  use Nvoi.Schema

  schema "note_mentions" do
    belongs_to :event, Nvoi.Event
    field :handled_at, :utc_datetime_usec
    belongs_to :user, Nvoi.User
    timestamps()
  end
end
