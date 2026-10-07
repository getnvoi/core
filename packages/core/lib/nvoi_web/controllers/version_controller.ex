defmodule NvoiWeb.VersionController do
  @moduledoc "The commit this build is: what `bin/deploy verify` compares with HEAD."
  use NvoiWeb, :controller

  def show(conn, _params),
    do: send_resp(conn, 200, Application.get_env(:nvoi, :version) || "dev")
end
