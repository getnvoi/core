defmodule NvoiWeb.Site do
  @moduledoc """
  The site host's own pages: `nvoi.to/` answers a blank page until the marketing site takes the root over at the
  edge, and the installation's own pages (`config :nvoi, :pages, {app, folder}`: each Markdown file of that folder of
  the app's priv at `/<name>`, not indexed), which answer on every host. An installation without the setting has none. Every other path goes on to the
  server: the API (`/api/v1`), the socket, the docs, the callbacks and webhooks GitHub and Stripe call. With no
  site host configured (development), the root is the server's.
  """

  @behaviour Plug
  import Plug.Conn

  @impl true
  def init(opts), do: opts

  @impl true
  def call(%Plug.Conn{path_info: []} = conn, _opts) do
    if conn.host == Application.get_env(:nvoi, :site_host) do
      conn
      |> put_resp_content_type("text/html")
      |> send_file(200, Application.app_dir(:nvoi, "priv/static/site/index.html"))
      |> halt()
    else
      conn
    end
  end

  def call(%Plug.Conn{method: "GET", path_info: [page]} = conn, _opts) do
    case path(page) do
      nil ->
        conn

      path ->
        conn
        |> put_resp_content_type("text/html")
        |> put_resp_header("x-robots-tag", "noindex")
        |> send_resp(200, page(path))
        |> halt()
    end
  end

  def call(conn, _opts), do: conn

  @doc "The file of the page `name`, or nil when the installation has no such page."
  def path(name) do
    with {app, folder} <- Application.get_env(:nvoi, :pages),
         true <- name =~ ~r/\A[a-z]+\z/,
         path = Application.app_dir(app, Path.join(["priv", folder, name <> ".md"])),
         true <- File.regular?(path) do
      path
    else
      _ -> nil
    end
  end

  @doc "A page as HTML: its front matter's title and date, its Markdown body."
  def page(path) do
    [_, front, body] =
      path
      |> File.read!()
      |> String.split("---\n", parts: 3)

    meta = YamlElixir.read_from_string!(front)
    title = Plug.HTML.html_escape(meta["title"])

    """
    <!doctype html>
    <html lang="en">
    <head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>#{title} · nvoi</title>
    <meta name="robots" content="noindex">
    <meta name="description" content="#{Plug.HTML.html_escape(meta["description"] || "")}">
    <style>
    body { margin: 0; background: #fff; color: #1c1917; font: 16px/1.65 -apple-system, "Segoe UI", sans-serif; }
    main { max-width: 720px; margin: 0 auto; padding: 48px 16px 80px; }
    h1 { font-size: 30px; margin: 0 0 6px; } h2 { font-size: 19px; margin: 36px 0 8px; }
    .updated { color: #78716c; font-size: 14px; margin: 0 0 32px; }
    a { color: inherit; } table { border-collapse: collapse; } td, th { border: 1px solid #e7e5e4; padding: 6px 10px; }
    </style>
    </head>
    <body><main>
    <h1>#{title}</h1>
    <p class="updated">Updated #{meta["updated"]}</p>
    #{MDEx.to_html!(body, extension: [table: true, autolink: true])}
    </main></body>
    </html>
    """
  end
end
