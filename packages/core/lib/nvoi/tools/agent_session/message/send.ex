defmodule Nvoi.Tools.AgentSession.Message.Send do
  @moduledoc "A message to the session's agent, with files; refused while a call waits on a decision."
  use Nvoi.Tools.Action,
    group: "agent_session",
    action: "message_send",
    category: :session,
    summary: "Send a prompt",
    post: "/sessions/{id}/messages",
    status: 201,
    access: :mutation,
    default_role: :drive,
    content: [:json, :multipart]

  param :id, :string, in: :path
  param :prompt, :string, required: true
  param :files, {:list, :file}

  def call(params, current) do
    session = Nvoi.Tools.AgentSession.Base.session(params, current)
    prompt = params["prompt"] || refuse!("prompt is required", "parameter_missing", 400)
    turn = Nvoi.Turns.say(session, prompt, user_id: current.user.id)

    for %Plug.Upload{} = file <- List.wrap(params["files"]) do
      blob =
        Nvoi.Storage.attach!(
          "Turn",
          turn.id,
          "files",
          file.filename,
          File.read!(file.path),
          file.content_type
        )

      if Nvoi.Thumbnail.pictures?(blob.content_type, blob.filename),
        do: Nvoi.Jobs.ThumbnailJob.for_file(turn.id, blob)
    end

    {:created, %{turn: NvoiWeb.TurnResource.wrap(turn, current.workspace.id)}}
  end
end
