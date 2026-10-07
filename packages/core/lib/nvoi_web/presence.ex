defmodule NvoiWeb.Presence do
  @moduledoc "Who has a session open, per `session:<id>` topic (NvoiWeb.SessionChannel)."
  use Phoenix.Presence, otp_app: :nvoi, pubsub_server: Nvoi.PubSub
end
