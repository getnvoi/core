defmodule NvoiWeb.Hooks.ConnectorsController do
  @moduledoc """
  POST /hooks/:connector: every delivery of a connector's service. No session: the signature over the exact bytes
  received stands in for it. Verify, store, apply, 200. A delivery id already held is applied again, since a retry
  and a replay look the same; its tasks fire only if it was never applied, so a delivery whose first apply failed
  still starts them. A failure answers 500; the tasks job applies again what a failure left unapplied, since
  services do not send it again by themselves.
  """
  use NvoiWeb, :controller
  alias Nvoi.{Connectors, Postgres, Webhook}

  def create(conn, params) do
    key = params["connector"] || conn.assigns.connector
    connector = Connectors.find(key)

    body =
      conn.assigns[:raw_body] ||
        with({:ok, body, _} <- read_body(conn), do: body, else: (_ -> ""))

    headers = Map.new(conn.req_headers)

    cond do
      connector == nil and key == "github" ->
        NvoiWeb.ApiController.refused(conn, Nvoi.GitHub.off())

      connector == nil ->
        send_resp(conn, 404, "")

      not Connectors.signed?(connector, body, headers) ->
        send_resp(conn, 401, "")

      true ->
        payload = payload(body)
        receive = connector.receiving()
        delivery = Connectors.read(payload, receive.delivery, headers)

        held =
          delivery && Postgres.get_by(Webhook, source: connector.key(), delivery_id: delivery)

        fresh = held == nil or held.processed_at == nil

        webhook =
          held ||
            Postgres.insert!(%Webhook{
              source: connector.key(),
              delivery_id: delivery,
              payload: payload,
              event: Connectors.read(payload, receive.event, headers),
              headers:
                Map.filter(headers, fn {name, _} ->
                  Enum.any?(receive.keep, &String.starts_with?(name, &1))
                end),
              received_at: DateTime.utc_now()
            })

        Connectors.Delivery.apply(webhook, fresh)
        send_resp(conn, 200, "")
    end
  end

  # A signed body that is not a json object is still kept, under raw: parsing it again will not help.
  defp payload(body) do
    case Jason.decode(body) do
      {:ok, %{} = parsed} -> parsed
      _ -> %{"raw" => body}
    end
  end
end
