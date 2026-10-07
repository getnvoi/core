defmodule NvoiWeb.UpController do
  use NvoiWeb, :controller

  def show(conn, _params) do
    Nvoi.Postgres.query!("select 1")
    send_resp(conn, 200, "up")
  end
end
