defmodule Nvoi.Tools.ArtifactVersion.Create do
  @moduledoc """
  A member's edit saved as the next version, named by them: `content` is the document as the editor holds it
  (Tiptap JSON). Unchanged content answers the newest version, 200; a new one, 201.
  """
  use Nvoi.Tools.Action,
    group: "artifact_version",
    action: "create",
    category: :artifact,
    summary: "Save a new version of an artifact",
    post: "/artifacts/{id}/versions",
    also: [201],
    refuses: [409],
    access: :mutation,
    default_role: :drive

  param :id, :string, in: :path
  param :content, :string, required: true
  alias Nvoi.Artifact
  alias Nvoi.Tools.Artifact.Base

  def call(params, current) do
    artifact = Base.artifact(params, current)
    medium = Artifact.medium(artifact)

    if not medium.editable,
      do: refuse!("A #{artifact.kind} is not edited here.", "not_editable", 409)

    if Artifact.archived?(artifact), do: refuse!("Restore it first.", "archived", 409)

    content =
      case params["content"] do
        nil ->
          refuse!("param is missing or the value is empty: content", "parameter_missing", 400)

        text when is_binary(text) ->
          text

        tree ->
          Jason.encode!(tree)
      end

    newest = Artifact.latest(artifact)

    message =
      params["message"]
      |> to_string()
      |> String.trim()
      |> String.slice(0, 200)
      |> Nvoi.Text.presence()

    saved =
      try do
        Artifact.save_version!(artifact, Artifact.Media.read(medium, content),
          user_id: current.user.id,
          message: message
        )
      rescue
        e in Artifact.Refused -> refuse!(e.message, "invalid_content", 422)
      end

    artifact =
      Postgres.update!(
        Ecto.Changeset.change(Postgres.reload!(artifact), served_number: saved.number)
      )

    Base.signal(artifact)
    body = Base.answer(artifact, current)
    if newest && saved.id == newest.id, do: body, else: {:created, body}
  end
end
