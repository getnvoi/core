defmodule NvoiWeb.TlsController do
  @moduledoc """
  The proxy in front asks it before getting a certificate for a name (on-demand TLS's `ask`):
  `GET /tls?domain=<name>` answers 200 for the installation's own names (`Nvoi.Hosts.own?/1`), else 404.
  """
  use NvoiWeb, :controller

  def show(conn, params) do
    if is_binary(params["domain"]) and Nvoi.Hosts.own?(String.downcase(params["domain"])),
      do: send_resp(conn, 200, ""),
      else: send_resp(conn, 404, "")
  end
end
