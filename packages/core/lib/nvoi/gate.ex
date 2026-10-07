defmodule Nvoi.Gate do
  @moduledoc """
  The tokens the preview worker verifies offline, HS256 with the shared secret (GATE_JWT_SECRET). HS256 is pinned on
  both sides, so a header naming another algorithm does not verify.

    %{aud: "preview", host, box, port, exp}   a worktree's app on `port`, or an artifact (`box` its id)
    %{aud: "terminal", host, box, exp}        one socket onto the machine's shells, minutes long

  An artifact token may carry `viewer` (`id, email, name`), the member looking, which the worker signs onto every
  request for the app; a visitor of a public link has none.
  """
  @audience "preview"
  @terminal "terminal"
  @expiry 2 * 3600
  @terminal_expiry 5 * 60
  # Two machines, two clocks: the worker allows the same.
  @leeway 60
  @header %{alg: "HS256", typ: "JWT"}

  defmodule Invalid, do: defexception([:message])

  def audience, do: @audience
  def expiry, do: @expiry
  def terminal, do: @terminal

  @doc "A preview token for this worktree's app on `port`."
  def token(worktree, port, expires_in \\ @expiry),
    do: sign(%{host: Nvoi.Preview.host(worktree), box: worktree.id, port: port}, expires_in)

  @doc "A terminal token for this worktree's machine."
  def terminal_token(worktree, expires_in \\ @terminal_expiry),
    do: sign(%{host: Nvoi.Preview.host(worktree), box: worktree.id}, expires_in, @terminal)

  @doc "A preview token for an artifact, for `viewer` (a user) or for nobody."
  def artifact_token(artifact, viewer, expires_in \\ @expiry) do
    claims = %{host: Nvoi.Artifact.host(artifact), box: artifact.id, port: artifact.port || 0}

    claims =
      if viewer,
        do:
          Map.put(claims, :viewer, %{
            id: viewer.id,
            email: viewer.email,
            name: Nvoi.Text.presence(viewer.github_login) || hd(String.split(viewer.email, "@"))
          }),
        else: claims

    sign(claims, expires_in)
  end

  def sign(claims, expires_in \\ @expiry, audience \\ @audience) do
    claims = Map.merge(claims, %{aud: audience, exp: now() + expires_in})
    signed = segment(@header) <> "." <> segment(claims)
    signed <> "." <> encode(mac(signed))
  end

  @doc """
  The claims in a token signed here, string-keyed. Raises `Invalid` for a bad signature, an audience not in
  `audiences` and an expired token alike: which of the three it was is nothing a caller acts on differently.
  """
  def verify(token, audiences \\ [@audience]) do
    with [header, payload, signature] <- String.split(to_string(token), ".", parts: 3),
         true <- Enum.all?([header, payload, signature], &(&1 != "")),
         %{"alg" => "HS256"} <- json(header),
         true <- Plug.Crypto.secure_compare(encode(mac(header <> "." <> payload)), signature),
         %{} = claims <- json(payload),
         true <- claims["aud"] in audiences,
         exp when is_integer(exp) <- claims["exp"],
         true <- exp > now() - @leeway do
      claims
    else
      _ -> raise Invalid, message: "invalid gate token"
    end
  end

  @doc "GATE_JWT_SECRET, else `gate_jwt_secret` in the credentials. Printable, since the worker reads it as text."
  def secret,
    do:
      Nvoi.Credentials.setting("gate_jwt_secret") ||
        raise(ArgumentError, "GATE_JWT_SECRET is not set")

  defp now, do: System.system_time(:second)
  defp mac(data), do: :crypto.mac(:hmac, :sha256, secret(), data)
  defp encode(bytes), do: Base.url_encode64(bytes, padding: false)
  defp segment(map), do: encode(Jason.encode!(map))

  defp json(part) do
    with {:ok, bytes} <- Base.url_decode64(part, padding: false),
         {:ok, %{} = map} <- Jason.decode(bytes),
         do: map,
         else: (_ -> nil)
  end
end
