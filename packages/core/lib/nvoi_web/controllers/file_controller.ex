defmodule NvoiWeb.FileController do
  @moduledoc "Serves a stored file by its signed path, resized when `size` asks."
  use Phoenix.Controller, formats: []
  import Plug.Conn
  alias Nvoi.Blob
  alias Nvoi.{Postgres, Storage}

  @sizes [256, 480, 960, 1600]

  def show(conn, %{"token" => token} = params) do
    with id when is_binary(id) <- Storage.blob_id(token),
         %Blob{} = blob <- Postgres.get(Blob, id) do
      blob =
        if (size = params["size"] && String.to_integer(params["size"])) in @sizes,
          do: Blob.variant!(blob, size),
          else: blob

      disposition = if params["disposition"] == "attachment", do: "attachment", else: "inline"

      conn
      |> put_resp_content_type(blob.content_type || "application/octet-stream", nil)
      |> put_resp_header("content-disposition", ~s(#{disposition}; filename="#{blob.filename}"))
      |> put_resp_header("cache-control", "max-age=31536000, private, immutable")
      |> send_resp(200, Storage.download(blob))
    else
      _ -> send_resp(conn, 404, "")
    end
  end
end
