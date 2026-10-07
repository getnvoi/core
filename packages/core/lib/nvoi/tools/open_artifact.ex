defmodule Nvoi.Tools.OpenArtifact do
  @moduledoc """
  Places an artifact's source in a folder of its own on the box, outside the checkout: the newest version when the
  artifact exists, else its kind's template. A folder already there is the agent's work in progress and is left as
  it is, unless members edit the kind and a newer version was saved since the folder was placed (`.version` names
  the one it holds): then the newest replaces it. What each kind is for reaches the agent's prompt from its medium.
  """
  use Nvoi.Tools.Base,
    name: "open_artifact",
    category: :artifact,
    icon: "package-open",
    box: true,
    label: "Open artifact",
    done: "Artifact opened"

  alias Compute.{Box, Layout}
  alias Nvoi.{AgentSession, Artifact, Postgres, Worktree}
  alias Nvoi.Artifact.{Media, Source}

  def instructions(_), do: Enum.map_join(Media.kinds(), "\n", &Media.fetch!(&1).instructions)

  def description(_),
    do:
      "Open an artifact to make or change it: something people look at, use or download, made beside the " <>
        "checkout. Its kind is #{Enum.join(Media.kinds(), " or ")} (app when left out). A new name starts from " <>
        "its kind's template, an existing name from its newest version. Answers the folder to work in; save it " <>
        "with save_artifact."

  def schema,
    do: %{
      type: "object",
      required: ["name"],
      properties: %{
        name: %{type: "string", description: "The artifact's name, as people will see it."},
        kind: %{
          type: "string",
          enum: Media.kinds(),
          description: "What it is, for a new artifact: #{Enum.join(Media.kinds(), ", ")}."
        }
      }
    }

  def call(input, %{worktree: worktree, session: session}) do
    name = input["name"] |> to_string() |> String.trim()
    if name == "", do: raise(Nvoi.Tools.Error, message: "name is required")
    kind = Nvoi.Text.presence(input["kind"] |> to_string() |> String.trim())

    if kind && kind not in Media.kinds(),
      do:
        raise(Nvoi.Tools.Error,
          message: "kind must be one of #{Enum.join(Media.kinds(), ", ")}, not #{inspect(kind)}"
        )

    artifact = find_or_create(AgentSession.workspace_id(session), name, kind || "app", session)

    if kind && artifact.kind != kind,
      do:
        raise(Nvoi.Tools.Error,
          message: "#{artifact.name} is an artifact of kind #{artifact.kind}, not #{kind}"
        )

    box = Worktree.box(worktree)
    dir = Artifact.dir(artifact)
    latest = Artifact.latest(artifact)

    newer =
      case Box.run(box, "test -d #{dir}") do
        {0, _} -> newer_than_folder(box, artifact, latest, dir) || :kept
        _ -> nil
      end

    if newer == :kept do
      "#{artifact.name} is already at #{dir}; the folder was kept."
    else
      source = if latest, do: Artifact.source(artifact, latest), else: Artifact.template(artifact)
      archive = "#{Layout.scratch()}/artifact-#{artifact.id}.tar.gz"
      Box.put(box, archive, source)

      Box.run!(
        box,
        """
        set -e
        rm -rf #{dir}
        mkdir -p #{dir}
        tar -xzf #{archive} -C #{dir} --no-same-owner
        #{Source.npm_cache()}
        """,
        as: Layout.user()
      )

      Box.run(box, "rm -f #{archive}")
      if Artifact.medium(artifact).editable, do: mark(box, dir, (latest && latest.number) || 0)
      from = if latest, do: "version #{latest.number}", else: "the template"
      from = if newer, do: "#{from}, which replaced the folder: #{newer}", else: from

      "#{artifact.name} (#{artifact.kind}) is at #{dir}, from #{from}. #{guide(artifact, dir)} " <>
        "Save it with save_artifact and the name #{inspect(artifact.name)}."
    end
  rescue
    e in Nvoi.Forge.Error ->
      raise Nvoi.Tools.Error,
        message: "the source of #{input["name"]} could not be read: #{Exception.message(e)}",
        plain: "The artifact store did not answer. Ask the agent to try again."
  end

  @doc "Notes in the folder which version it holds."
  def mark(box, dir, number),
    do: Box.run(box, "printf %s #{number} > #{dir}/.version", as: Layout.user())

  # Why the newest version replaces the folder, or nil: only for a kind members edit, when a version newer than the
  # one the folder holds was saved.
  defp newer_than_folder(box, artifact, newest, dir) do
    if Artifact.medium(artifact).editable and newest do
      {_, held} = Box.run(box, "cat #{dir}/.version 2>/dev/null")

      held =
        case Integer.parse(String.trim(to_string(held))) do
          {n, _} -> n
          _ -> 0
        end

      if held < newest.number do
        by =
          if newest.user_id,
            do: "#{Nvoi.User.name(Postgres.get!(Nvoi.User, newest.user_id))} saved it",
            else: "it was saved"

        "#{by} as version #{newest.number} after this folder was placed"
      end
    end
  end

  # How to work in the folder: an app's tree is built with npm, a medium of one file is that file.
  defp guide(artifact, dir) do
    medium = Artifact.medium(artifact)

    cond do
      length(medium.inputs) > 1 ->
        "Edit #{Enum.map_join(medium.inputs, " or ", &"#{dir}/#{&1}")}; the one changed last is the whole artifact."

      medium.file ->
        "Write #{dir}/#{medium.file}; it is the whole artifact."

      true ->
        "Read #{dir}/CLAUDE.md first. Run npm there with nvoi's Node first on the PATH " <>
          "(`export PATH=#{Layout.node_home()}/bin:$PATH`), then `npm install`."
    end
  end

  defp find_or_create(workspace_id, name, kind, session) do
    case Artifact.named(workspace_id, name) do
      %Artifact{archived_at: nil} = found ->
        found

      %Artifact{} ->
        raise Nvoi.Tools.Error,
          message: "#{name} is archived",
          plain: "#{name} is archived. Restore it first."

      nil ->
        try do
          artifact = Artifact.create!(workspace_id, name, kind, session.id)
          Nvoi.Live.signal(workspace_id, "artifacts")
          artifact
        rescue
          Ecto.InvalidChangesetError -> Artifact.named(workspace_id, name)
        end
    end
  end
end
