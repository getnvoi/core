defmodule Nvoi.Schema do
  @moduledoc "What every model shares: a uuid v7 id the database assigns, and `created_at` and `updated_at`."
  defmacro __using__(_) do
    quote do
      use Ecto.Schema
      import Ecto.Changeset
      import Ecto.Query, warn: false
      @primary_key {:id, :binary_id, read_after_writes: true}
      @foreign_key_type :binary_id
      @timestamps_opts [inserted_at: :created_at, type: :utc_datetime_usec]
    end
  end
end
