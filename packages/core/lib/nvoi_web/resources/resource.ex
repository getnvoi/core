defmodule NvoiWeb.Resource do
  @moduledoc "The JSON shape of one noun: `one/1` for a record, `wrap/1` for a record or a list."
  defmacro __using__(_) do
    quote do
      import Ecto.Query, warn: false
      def wrap(list) when is_list(list), do: Enum.map(list, &one/1)
      def wrap(nil), do: nil
      def wrap(record), do: one(record)
    end
  end
end
