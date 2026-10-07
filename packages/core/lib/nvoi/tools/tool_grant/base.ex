defmodule Nvoi.Tools.ToolGrant.Base do
  @moduledoc "One member's rows for the tool gateway. Anyone reads their own; another's, and every change, is an admin's."
  def member(params, current),
    do: Nvoi.Tools.Record.find!(Nvoi.WorkspaceUser.live(), params, current)
end
