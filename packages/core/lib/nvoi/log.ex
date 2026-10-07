defmodule Nvoi.Log do
  use Nvoi.Schema

  schema "logs" do
    field :record_id, :binary_id
    field :record_type, :string
    field :output, :string
    field :step, :string
    timestamps()
  end
end
