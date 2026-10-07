defmodule Nvoi.Encryption do
  @moduledoc """
  The columns' encryption: AES-256-GCM, a key derived with PBKDF2-SHA256 from the primary
  key and `key_derivation_salt`, a JSON message, the payload zlib-compressed when `h.c` is set.
  """

  @iterations 65_536

  def decrypt(message) do
    %{"p" => payload, "h" => %{"iv" => iv, "at" => tag} = headers} = Jason.decode!(message)

    Enum.find_value(keys(), fn key ->
      case :crypto.crypto_one_time_aead(
             :aes_256_gcm,
             key,
             b64(iv),
             b64(payload),
             "",
             b64(tag),
             false
           ) do
        :error -> nil
        clear -> if headers["c"], do: :zlib.uncompress(clear), else: clear
      end
    end) || raise ArgumentError, "the message does not decrypt with any primary key"
  end

  def encrypt(clear) do
    iv = :crypto.strong_rand_bytes(12)
    {compressed, clear} = compress(clear)
    {payload, tag} = :crypto.crypto_one_time_aead(:aes_256_gcm, hd(keys()), iv, clear, "", true)
    headers = %{"iv" => Base.encode64(iv), "at" => Base.encode64(tag)}

    Jason.encode!(%{
      "p" => Base.encode64(payload),
      "h" => if(compressed, do: Map.put(headers, "c", true), else: headers)
    })
  end

  # A value over 140 bytes is compressed when that makes it smaller.
  defp compress(clear) when byte_size(clear) > 140 do
    compressed = :zlib.compress(clear)
    if byte_size(compressed) < byte_size(clear), do: {true, compressed}, else: {false, clear}
  end

  defp compress(clear), do: {false, clear}

  # The last primary key encrypts; every one decrypts, newest first.
  defp keys do
    with nil <- :persistent_term.get(__MODULE__, nil) do
      salt = Nvoi.Credentials.fetch!(["encryption", "key_derivation_salt"])

      keys =
        ["encryption", "primary_key"]
        |> Nvoi.Credentials.fetch!()
        |> List.wrap()
        |> Enum.reverse()
        |> Enum.map(&derive(&1, salt))

      :persistent_term.put(__MODULE__, keys)
      keys
    end
  end

  defp derive(password, salt), do: :crypto.pbkdf2_hmac(:sha256, password, salt, @iterations, 32)
  defp b64(part), do: Base.decode64!(part)
end
