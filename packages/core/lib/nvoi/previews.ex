defmodule Nvoi.Previews do
  @moduledoc """
  Whether this installation serves previews: the preview worker on `*<preview suffix>`, which shows a session's
  app, an app artifact and the Terminal, and asks the server on its sandbox host. On when `config :nvoi, :previews`
  says so, or when the `previews` secret is found (`Nvoi.Credentials.setting/1`: the file the self-hosted previews
  service writes once the worker is deployed). Off, what needs the worker refuses with 501 (`refuse!/0`).
  """

  @message "Previews are not set up on this server. Its admin turns them on with the previews service in " <>
             "compose.yml (Cloudflare)."

  def enabled?,
    do:
      Application.get_env(:nvoi, :previews, false) or Nvoi.Credentials.setting("previews") != nil

  def message, do: @message

  @doc "Refuses (`Nvoi.Tools.Action.Refused`, 501 `previews_not_set_up`) unless previews are on."
  def refuse! do
    if not enabled?(), do: Nvoi.Tools.Action.refuse!(@message, "previews_not_set_up", 501)
    :ok
  end
end
