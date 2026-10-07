defmodule NvoiWeb.OutputController do
  @moduledoc """
  A version's output served from storage, for a medium that runs nothing on a box (a page, a document, a sheet, an
  image, a video's player): its bytes, or the page its codec writes for them, on the token `Nvoi.Artifact.output_path/2`
  signed. No sign-in and no cookie: the token is the permission, for a member's viewer as for a public link's
  visitor. The page is the agent's, on this app's own host, so it is served in a CSP sandbox without
  `allow-same-origin`: it runs in an opaque origin and reads no cookie or storage of this host.

  `public` opens a public artifact's link: the version shown, wherever it is served.
  """
  use Phoenix.Controller, formats: []
  import Plug.Conn
  alias Nvoi.{Artifact, Postgres}
  alias Nvoi.Artifact.Media

  def show(conn, %{"token" => token}) do
    with {artifact, version} <- Artifact.output(token),
         medium = Artifact.medium(artifact),
         true <- Media.stored?(medium),
         page when page != nil <- Artifact.page(artifact, version) do
      type = if medium.codec || medium.preview, do: "text/html", else: medium.content_type

      conn
      |> put_resp_header("content-security-policy", "sandbox #{medium.sandbox}")
      |> put_resp_header("x-robots-tag", "noindex")
      |> put_resp_header("referrer-policy", "no-referrer")
      |> put_resp_header("x-content-type-options", "nosniff")
      |> put_resp_header("cache-control", "private, max-age=300")
      |> put_resp_header(
        "content-disposition",
        ~s(inline; filename="#{artifact.slug}-v#{version.number}#{Media.page_format(medium)}")
      )
      |> put_resp_content_type(type, "utf-8")
      |> send_resp(200, page)
    else
      _ -> send_resp(conn, 404, "")
    end
  end

  # A customer's page, not ours: search engines leave it out.
  def public(conn, %{"id" => id}) do
    with {:ok, id} <- Ecto.UUID.cast(id),
         %Artifact{public: true, archived_at: nil} = artifact <- Postgres.get(Artifact, id),
         %{} <- Artifact.latest(artifact) do
      conn
      |> put_resp_header("x-robots-tag", "noindex")
      |> go(Artifact.url_for(artifact, nil))
    else
      _ -> send_resp(conn, 404, "")
    end
  end

  # An app on its own host; an output on this one, by its path, so the visitor stays on the host they asked.
  defp go(conn, "https://" <> _ = url), do: redirect(conn, external: url)
  defp go(conn, path), do: redirect(conn, to: path)
end
