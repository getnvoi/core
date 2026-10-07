defmodule Nvoi.Tools.Utils.File.List do
  @moduledoc """
  The checkout's paths, read on its machine, with what the branch changed.
  """
  use Nvoi.Tools.Action,
    group: "utils",
    action: "file_list",
    category: :session,
    summary: "List the checkout's paths",
    get: "/sessions/{id}/files",
    refuses: [409],
    access: :free,
    default_role: :view

  param :id, :string, in: :path
  alias Nvoi.Tools.AgentSession.Base

  def call(params, current) do
    session = params |> Base.session(current)

    Base.on_box(session, fn box, worktree ->
      listing = Nvoi.Files.list(box)

      changed =
        for f <- Nvoi.Worktree.changed_files(worktree), do: %{path: f.name, status: f.status}

      Map.put(listing, :changed, changed)
    end)
  end
end
