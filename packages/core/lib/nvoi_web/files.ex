defmodule NvoiWeb.Files do
  @moduledoc """
  The site's files and pages, the router's last route: the static files (`NvoiWeb.static_paths/0`), the docs built
  from packages/core/site, the root page and the legal pages (NvoiWeb.Site); a 404 for anything else. The docs' pages are
  folders (`/docs/guides/x/`): a folder answers its index.html, and `/docs` moves to `/docs/` so the pages' relative
  links resolve.
  """
  @behaviour Plug
  import Plug.Conn

  @impl true
  def init(_) do
    gzip =
      not Keyword.get(Application.get_env(:nvoi, NvoiWeb.Endpoint, []), :code_reloader, false)

    %{
      files: Plug.Static.init(at: "/", from: :nvoi, gzip: gzip, only: NvoiWeb.static_paths()),
      docs: Plug.Static.init(at: "/docs", from: {:nvoi, "priv/static/docs"}, gzip: false)
    }
  end

  @impl true
  def call(%Plug.Conn{request_path: "/docs"} = conn, _),
    do: conn |> Phoenix.Controller.redirect(to: "/docs/") |> halt()

  def call(conn, static) do
    conn
    |> serve(&Plug.Static.call(&1, static.files))
    |> serve(&Plug.Static.call(folder(&1), static.docs))
    |> serve(&NvoiWeb.Site.call(&1, []))
    |> serve(&send_resp(&1, 404, ""))
  end

  defp serve(%Plug.Conn{halted: true} = conn, _), do: conn
  defp serve(%Plug.Conn{state: state} = conn, _) when state != :unset, do: conn
  defp serve(conn, step), do: step.(conn)

  defp folder(%Plug.Conn{path_info: ["docs" | _], request_path: path} = conn) do
    if String.ends_with?(path, "/"),
      do: %{conn | path_info: conn.path_info ++ ["index.html"]},
      else: conn
  end

  defp folder(conn), do: conn
end
