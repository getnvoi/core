defmodule Nvoi.Tools.BuildImage do
  @moduledoc """
  The agent asks for its checkout's pushed commit to be built into an image for the repository's production
  environment, in that environment's build machine (Nvoi.Image.ensure!/3, as the image build action does); it waits
  for the build and reads the result: the image, or the build's last lines to fix the Dockerfile. The branch must be
  pushed first (push_branch): the source comes from the repository, never from the machine. The agent never runs on
  production and sees none of its variables; the build machine holds none.
  """
  use Nvoi.Tools.Base,
    name: "build_image",
    category: :repo,
    icon: "package",
    target: "environment",
    done: "Image built"

  import Ecto.Query
  alias Nvoi.{Image, Postgres}

  # How long the call waits for the build before answering that it still runs (the agent calls it again).
  @wait 600
  # Lines of a failed build the agent reads.
  @failed_lines 40

  def description(_),
    do:
      "Build the pushed head of this checkout's branch into a container image from its root Dockerfile, for the " <>
        "repository's production environment, and wait for the result. Push the branch first with push_branch. " <>
        "On failure, read the build's last lines, fix the Dockerfile, push, and call it again."

  def schema,
    do: %{
      type: "object",
      properties: %{
        environment: %{
          type: "string",
          description: "The production environment's name, when the repository has several"
        }
      },
      required: []
    }

  def call(input, %{worktree: worktree, session: session}) do
    repo = Postgres.get!(Nvoi.Repo, worktree.repo_id)
    environment = environment!(repo, input["environment"])
    Nvoi.Storage.Bucket.ready!()

    commit =
      Image.resolve(repo, worktree.branch) ||
        raise Nvoi.Tools.Error,
          message:
            "#{worktree.branch} is not pushed: call push_branch first, then build_image again.",
          plain: "The agent asked for an image of a branch it had not pushed."

    image =
      environment
      |> Image.ensure!(commit, %{agent_session_id: session.id})
      |> Image.await(@wait * 1000)

    case image.status do
      "ready" ->
        "image #{image.id} of #{String.slice(commit, 0, 12)} for #{environment.name} is ready: " <>
          "#{image.digest}, #{image.size} bytes"

      "failed" ->
        lines =
          image |> Image.lines() |> Enum.take(-@failed_lines) |> Enum.map_join("\n", & &1.output)

        raise Nvoi.Tools.Error,
          message: "the build of #{String.slice(commit, 0, 12)} failed: #{image.error}\n#{lines}",
          plain:
            "The image of #{worktree.branch} did not build. The agent has the build's last lines."

      status ->
        "the image #{image.id} of #{String.slice(commit, 0, 12)} is still #{status}: call build_image again to wait for it"
    end
  rescue
    e in Nvoi.Tools.Action.Refused ->
      raise Nvoi.Tools.Error, message: e.message, plain: e.message
  end

  defp environment!(repo, name) do
    production =
      Postgres.all(
        from e in Nvoi.Environment.live(),
          where: e.repo_id == ^repo.id and e.kind == "production",
          order_by: e.name
      )

    case {production, Nvoi.Text.presence(name)} do
      {[], _} ->
        raise Nvoi.Tools.Error,
          message:
            "#{repo.name} has no production environment: images are built for production only.",
          plain:
            "The agent asked for an image, and this repository has no production environment."

      {[one], nil} ->
        one

      {several, nil} ->
        raise Nvoi.Tools.Error,
          message:
            "#{repo.name} has several production environments: name one in environment (#{Enum.map_join(several, ", ", & &1.name)}).",
          plain: "The agent did not say which production environment to build for."

      {several, name} ->
        Enum.find(several, &(&1.name == name)) ||
          raise Nvoi.Tools.Error,
            message:
              "#{name} is not a production environment of #{repo.name}: #{Enum.map_join(several, ", ", & &1.name)}.",
            plain: "The agent named an environment that is not production."
    end
  end
end
