defmodule NvoiWeb.ArtifactResource do
  @moduledoc """
  `kind` names the artifact's medium; `viewer` is how a client shows it ("webview"), `runs` whether it runs on a box
  (an app: `state` says how its start went) or is served from storage (always ready), and `exports` what a version
  downloads as. `served_number` is the version shown. `url` opens it for the member asking: an app's link while it
  can run (it has a version, is live and its session's checkout still exists, `session_exists`), or a stored
  version's output, a path on the API's host; otherwise nil. `failure` is what the last failed start said while it
  is failed. `public_url` is the link anyone opens without signing in, while it is public and live. Each version
  names its `author` (the member who saved it; nil for the agent) and, for each export built on the box, where it
  stands (`exports`: extension, state none, building, ready or failed, `error` when failed, `url` when ready, and
  `progress` from 0 to 100 while a long build reports it).
  """
  use NvoiWeb.Resource
  alias Nvoi.{Artifact, ArtifactVersion, Postgres}
  alias Nvoi.Artifact.Media

  def one(artifact, viewer \\ nil) do
    medium = Artifact.medium(artifact)
    versions = Artifact.versions(artifact)
    latest = List.last(versions)

    users =
      Map.new(
        Postgres.all(
          from u in Nvoi.User,
            where: u.id in ^(versions |> Enum.map(& &1.user_id) |> Enum.reject(&is_nil/1))
        ),
        &{&1.id, &1}
      )

    shown = Enum.find(versions, &(&1.number == artifact.served_number)) || latest
    archived = Artifact.archived?(artifact)

    %{
      id: artifact.id,
      name: artifact.name,
      kind: artifact.kind,
      viewer: medium.viewer,
      runs: Media.service?(medium),
      exports: medium.exports,
      editable: medium.editable,
      state: artifact.state,
      shared: artifact.shared,
      public: artifact.public,
      public_url: Artifact.public_url(artifact),
      session_id: artifact.session_id,
      session_exists: artifact.worktree_id != nil,
      archived: archived,
      served_number: shown && shown.number,
      url:
        if(latest && not archived && (artifact.worktree_id || Media.stored?(medium)),
          do: Artifact.url_for(artifact, viewer)
        ),
      failure: Artifact.failure(artifact),
      thumbnail_url: latest && ArtifactVersion.thumbnail_path(latest),
      versions:
        for v <- versions do
          %{
            number: v.number,
            message: v.message,
            created_at: v.created_at,
            author: v.user_id && NvoiWeb.AuthorResource.wrap(users[v.user_id]),
            output_url:
              if(Media.stored?(medium) and not archived, do: Artifact.output_path(artifact, v)),
            thumbnail_url: ArtifactVersion.thumbnail_path(v),
            exports: for(format <- Media.built(medium), do: export(v, format))
          }
        end,
      created_at: artifact.created_at,
      updated_at: artifact.updated_at
    }
  end

  def wrap(list, viewer) when is_list(list), do: Enum.map(list, &one(&1, viewer))
  def wrap(record, viewer), do: one(record, viewer)

  defp export(version, format) do
    {state, error} = ArtifactVersion.export_state(version, format)
    file = state == "ready" && ArtifactVersion.export_file(version, format)

    %{
      extension: format,
      state: state,
      error: error,
      url: if(file, do: Nvoi.Storage.path(file, disposition: "attachment")),
      progress: ArtifactVersion.export_progress(version, format)
    }
  end
end
