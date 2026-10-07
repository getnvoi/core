defmodule Nvoi.Tools.Utils.PreviewCall.Answer do
  @moduledoc """
  What the app did with a call it performed in the preview: `text`, `error` when it failed, and for a screenshot
  `image` (a PNG, base64) of the page at `url`. The agent reads it next. A call already answered, or failed at
  its deadline, is a 409.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "preview_call_answer",
    category: :preview,
    summary: "Answer what the agent asked of the preview",
    post: "/sessions/{id}/preview/calls/{call_id}",
    status: 204,
    refuses: [409],
    access: :mutation,
    default_role: :drive

  param :call_id, :string, in: :path
  param :id, :string, in: :path
  param :error, :boolean
  param :image, :string, doc: "A screenshot's PNG, base64"
  param :text, :string, required: true
  param :url, :string, doc: "The page's address when the screenshot was taken"
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    call = find!(params, current)
    text = params["text"]
    if not is_binary(text), do: refuse!("text is required", "parameter_missing", 400)

    image =
      case params["image"] do
        nil ->
          nil

        data ->
          case Elixir.Base.decode64(to_string(data)) do
            {:ok, png} -> png
            :error -> refuse!("image must be a PNG in base64", "invalid_image", 422)
          end
      end

    if not Nvoi.Browser.answer(
         call,
         text,
         params["error"] in [true, "true"],
         image,
         params["url"]
       ),
       do: refuse!("That call was already answered or timed out.", "already_answered", 409)

    nil
  end

  @doc "A call of the session in the reader's reach, waiting for the preview."
  def find!(params, current) do
    session = Base.session(params, current)

    Postgres.one(from e in Nvoi.Browser.waiting(session), where: e.id == ^params["call_id"]) ||
      refuse!("That call was already answered or timed out.", "already_answered", 409)
  end
end
