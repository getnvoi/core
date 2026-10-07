defmodule Nvoi.Json do
  @moduledoc "A jsonb column holding any JSON value: an object, a list or a scalar."
  use Ecto.Type

  def type, do: :map
  def cast(value), do: {:ok, value}
  def load(value), do: {:ok, value}
  def dump(value), do: {:ok, value}
end
