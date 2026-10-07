defmodule Nvoi.Webhook do
  use Nvoi.Schema

  schema "webhooks" do
    field :delivery_id, :string
    field :event, :string
    field :headers, Nvoi.Json, default: %{}
    field :outcomes, Nvoi.Json, default: %{}
    field :payload, Nvoi.Json, default: %{}
    field :processed_at, :utc_datetime_usec
    field :received_at, :utc_datetime_usec
    field :source, :string
    timestamps()
  end
end
