defmodule NvoiWeb.Hosts do
  @moduledoc """
  The site's side of production's hosts, in the site routes' pipeline (NvoiWeb.Router declares the rest: the git,
  MCP and sandbox hosts each serve their part alone, before the site's routes are tried):

  | host | answer |
  | --- | --- |
  | the site (`nvoi.to`) | its routes |
  | a preview's name (`*-preview.nvoi.to`) | passes |
  | a declared host (`git.`, `mcp.`, `sandbox.`) on a path that is not its own | 404 |
  | any other name of the domain (`www.nvoi.to`, …) | a permanent redirect to the site, same path |
  | an address, or any other name | `/up` alone, the pods' health check |

  Elsewhere (dev, a stack, the tests) it passes everything.
  """
  import Plug.Conn

  def init(opts), do: opts

  def call(conn, part) do
    cond do
      not Application.get_env(:nvoi, :production, false) -> conn
      part == :sandbox -> sandbox(conn)
      true -> guard(conn)
    end
  end

  # The preview worker's questions, on its host and nowhere else.
  defp sandbox(conn) do
    if conn.host == Application.get_env(:nvoi, :sandbox_host),
      do: conn,
      else: conn |> send_resp(404, "") |> halt()
  end

  defp guard(conn) do
    site = Application.get_env(:nvoi, :site_host)
    declared = [:git_host, :mcp_host, :sandbox_host] |> Enum.map(&Application.get_env(:nvoi, &1))

    cond do
      conn.host == site ->
        conn

      String.ends_with?(conn.host, Application.get_env(:nvoi, :preview_suffix)) ->
        conn

      conn.host in declared ->
        conn |> send_resp(404, "") |> halt()

      String.ends_with?(conn.host, "." <> site) ->
        query = if conn.query_string == "", do: "", else: "?" <> conn.query_string

        conn
        |> put_resp_header("location", "https://#{site}#{conn.request_path}#{query}")
        |> send_resp(301, "")
        |> halt()

      conn.path_info == ["up"] ->
        conn

      true ->
        conn |> send_resp(404, "") |> halt()
    end
  end

  @doc "Whether the live socket may connect on this host: the site's alone in production."
  def socket?(host),
    do:
      not Application.get_env(:nvoi, :production, false) or
        host == Application.get_env(:nvoi, :site_host)
end
