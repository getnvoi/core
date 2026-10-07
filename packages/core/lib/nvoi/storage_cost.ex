defmodule Nvoi.StorageCost do
  @moduledoc """
  What a workspace's stored bytes cost on one day, per category and per environment where one applies: the bytes
  measured that day times the storage rate, booked once a day (`Nvoi.Jobs.StorageCostJob`), debited from credit
  like machine time (`[:nvoi, :costs, :booked]`) and shown by `Nvoi.Usage` beside machines and runs.

  A category is a function here that measures its bytes (`categories/0`); adding one adds no column. Today:
  `images`, the tarballs a production environment keeps in nvoi's bucket (ready images; a pruned one no longer
  counts).
  """
  use Nvoi.Schema

  schema "storage_costs" do
    field :category, :string
    field :day, :date
    field :bytes, :integer
    field :cents, :decimal
    field :billed, :boolean, default: false
    belongs_to :workspace, Nvoi.Workspace
    belongs_to :environment, Nvoi.Environment
    timestamps()
  end

  alias Nvoi.Postgres

  # Cents per GB stored for a month: what R2 charges for standard storage ($0.015 per GB-month).
  @cents_per_gb_month 1.5
  @gb 1_073_741_824

  def cents_per_gb_month, do: @cents_per_gb_month

  @doc "Each category and the function measuring it: `[%{workspace_id, environment_id, bytes}]` as stored now."
  def categories, do: %{"images" => &images/0}

  @doc "The ready images' bytes, per production environment."
  def images do
    Postgres.all(
      from i in Nvoi.Image,
        join: e in Nvoi.Environment,
        on: e.id == i.environment_id,
        join: r in Nvoi.Repo,
        on: r.id == e.repo_id,
        where: i.status == "ready" and not is_nil(i.size),
        group_by: [r.workspace_id, i.environment_id],
        select: %{
          workspace_id: r.workspace_id,
          environment_id: i.environment_id,
          bytes: type(sum(i.size), :integer)
        }
    )
  end

  @doc "The day's costs, measured now, once per workspace, category and environment: a second run that day adds none."
  def book!(day \\ Date.utc_today()) do
    month = Date.days_in_month(day)

    for {category, measure} <- categories(), row <- measure.(), row.bytes > 0 do
      cents =
        Decimal.from_float(row.bytes / @gb * @cents_per_gb_month / month) |> Decimal.round(6)

      Postgres.insert!(
        %__MODULE__{
          workspace_id: row.workspace_id,
          environment_id: row.environment_id,
          category: category,
          day: day,
          bytes: row.bytes,
          cents: cents
        },
        on_conflict: :nothing,
        conflict_target: [:workspace_id, :category, :environment_id, :day]
      )
    end

    :ok
  end
end
