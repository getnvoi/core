defmodule Nvoi.Connectors.Signature do
  @moduledoc """
  How a delivery proves it came from its service, one scheme per way services sign. Each compares in constant time
  over the exact bytes received.
  """

  @doc "An HMAC-SHA256 hex digest of the body, behind the scheme's prefix (GitHub's `sha256=`)."
  def verify(:hmac_hex, body, sent, secret, prefix) do
    expected = prefix <> Base.encode16(:crypto.mac(:hmac, :sha256, secret, body), case: :lower)
    Plug.Crypto.secure_compare(expected, to_string(sent))
  end
end
