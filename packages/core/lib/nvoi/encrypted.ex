defmodule Nvoi.Encrypted do
  @moduledoc "An Ecto type for an encrypted column: the clear value in Elixir, a `Nvoi.Encryption` message stored."
  use Ecto.Type

  def type, do: :string
  def cast(value) when is_binary(value) or is_nil(value), do: {:ok, value}
  def cast(_), do: :error
  def load(nil), do: {:ok, nil}
  def load(message), do: {:ok, Nvoi.Encryption.decrypt(message)}
  def dump(nil), do: {:ok, nil}
  def dump(clear), do: {:ok, Nvoi.Encryption.encrypt(clear)}
end
