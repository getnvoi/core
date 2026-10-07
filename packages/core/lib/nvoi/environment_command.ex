defmodule Nvoi.EnvironmentCommand do
  use Nvoi.Schema

  schema "environment_commands" do
    field :archived_at, :utc_datetime_usec
    field :before, {:array, :string}, default: []
    field :command, :string
    belongs_to :environment, Nvoi.Environment
    field :kind, :string, default: "default"
    field :name, :string
    field :port, :integer
    field :position, :integer, default: 0
    timestamps()
  end

  @doc "The rows nobody archived: an archived one is out of every list and every reach."
  def live, do: from(r in __MODULE__, where: is_nil(r.archived_at))

  @reserved_ports [8080]

  @doc "setup runs after the clone and exits; preview stays up; default is run on demand."
  def changeset(command, attrs) do
    command
    |> cast(attrs, [:name, :command, :kind, :port, :before, :position])
    |> update_change(:name, &String.trim/1)
    |> update_change(:command, &String.trim/1)
    |> update_change(:before, fn list ->
      list |> List.wrap() |> Enum.map(&String.trim(to_string(&1))) |> Enum.reject(&(&1 == ""))
    end)
    |> validate_required([:name, :command])
    |> validate_inclusion(:kind, ~w(setup preview default),
      message: "is not included in the list"
    )
    |> validate_number(:port, greater_than_or_equal_to: 1, less_than_or_equal_to: 65_535)
    |> validate_exclusion(:port, @reserved_ports, message: "is the preview proxy's")
    |> unsafe_validate_unique([:name, :environment_id], Nvoi.Postgres, query: live())
    |> then(fn c ->
      if get_field(c, :kind) == "preview",
        do: c,
        else:
          Enum.reduce([:port, :before], c, fn f, acc ->
            if get_field(acc, f) in [nil, []],
              do: acc,
              else: add_error(acc, f, "is for preview commands only")
          end)
    end)
  end

  def listed(environment_id) do
    for kind <- ~w(setup preview default),
        c <-
          Nvoi.Postgres.all(
            from c in live(),
              where: c.environment_id == ^environment_id and c.kind == ^kind,
              order_by: [c.position, c.name]
          ),
        do: c
  end
end
