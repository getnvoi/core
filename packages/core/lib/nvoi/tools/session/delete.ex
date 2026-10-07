defmodule Nvoi.Tools.Session.Delete do
  @moduledoc "The controller signs out the session making the request."
  use Nvoi.Tools.Action,
    group: "session",
    action: "delete",
    category: :account,
    summary: "Revoke the token",
    delete: "/logout",
    status: 204,
    access: :sensitive,
    default_role: :open

  def call(_params, _current), do: nil
end
