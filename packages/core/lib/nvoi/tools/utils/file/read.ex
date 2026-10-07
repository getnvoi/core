defmodule Nvoi.Tools.Utils.File.Read do
  @moduledoc """
  One file of the checkout: its text, or why it is not shown.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "file_read",
    category: :session,
    summary: "Read one file of the checkout",
    get: "/sessions/{id}/file",
    refuses: [400, 404],
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  param :path, :string, in: :query
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    session = params |> Base.session(current)
    path = params["path"] || refuse!("path is required", "parameter_missing", 400)
    blob = Base.on_box(session, fn box, _ -> Nvoi.Files.read(box, path) end)
    if blob.refused == "missing", do: {:not_found, blob}, else: blob
  end
end
