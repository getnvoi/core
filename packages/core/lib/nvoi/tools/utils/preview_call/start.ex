defmodule Nvoi.Tools.Utils.PreviewCall.Start do
  @moduledoc """
  An app takes a waiting call to perform it in its preview. One app performs each call: the first to take it;
  the others are refused and only watch.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "preview_call_start",
    category: :preview,
    summary: "Pick up a call to perform it in the preview",
    post: "/sessions/{id}/preview/calls/{call_id}/start",
    refuses: [409],
    access: :mutation,
    default_role: :drive

  param :call_id, :string, in: :path
  param :id, :string, in: :path
  alias Nvoi.Tools.Utils.PreviewCall.Answer

  def call(params, current) do
    call = Answer.find!(params, current)

    if not Nvoi.Browser.pick(call),
      do: refuse!("Another app already performs this call.", "already_picked", 409)

    %{call: NvoiWeb.PreviewCallResource.wrap(Postgres.reload!(call))}
  end
end
