defmodule Nvoi.Tools.Environment.Create do
  use Nvoi.Tools.Action,
    group: "environment",
    action: "create",
    category: :environment,
    summary: "Create an environment",
    post: "/repos/{repo_id}/environments",
    status: 201,
    refuses: [422],
    access: :mutation,
    locked: true,
    default_role: :manage

  param :repo_id, :string, in: :path
  param :branch, :string
  param :database_engine, :string
  param :database_version, :string
  param :disk, :integer
  param :name, :string, required: true
  param :preview_port, :integer
  param :provider_name, :string
  param :shape, :string

  param :kind, :string,
    values: Nvoi.Environment.kinds(),
    doc: "development (the default) or production"

  alias Nvoi.Tools.Environment.Base

  def call(params, current) do
    repo = Base.repo(params, current)

    attrs =
      Map.take(
        params,
        ~w(name provider_name shape disk branch preview_port database_engine database_version)
      )

    kind = Nvoi.Text.presence(to_string(params["kind"] || "")) || "development"

    environment =
      Nvoi.Environment.save!(
        Nvoi.Environment.changeset(%Nvoi.Environment{repo_id: repo.id, kind: kind}, attrs, repo)
      )

    Nvoi.Environment.resolve_manifest_later(environment)
    {:created, Base.answer(environment)}
  end
end
