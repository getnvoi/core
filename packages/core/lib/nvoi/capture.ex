defmodule Nvoi.Capture do
  use Nvoi.Schema

  schema "captures" do
    field :description, :string
    field :label, :string
    belongs_to :turn, Nvoi.Turn
    field :url, :string
    field :kind, :string, default: "screenshot"
    field :digest, :string
    timestamps()
  end
end
