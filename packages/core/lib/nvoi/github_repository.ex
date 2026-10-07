defmodule Nvoi.GitHubRepository do
  use Nvoi.Schema

  schema "github_repositories" do
    field :full_name, :string
    field :github_id, :integer
    belongs_to :github_installation, Nvoi.GitHubInstallation
    field :private, :boolean
    field :pushed_at, :utc_datetime_usec
    timestamps()
  end
end
