defmodule Nvoi.Storage.Bucket do
  @moduledoc """
  nvoi's bucket (R2, S3's API) reached by URLs signed here for one object, one method and a short while (AWS
  Signature Version 4, query form): whoever holds one reads or writes that object and nothing else, and holds no
  credential. Build machines upload images' tarballs and cluster nodes download them this way, so their bytes never
  pass through nvoi's server. The bucket and its key pair are `NVOI_BUCKET`, `NVOI_BUCKET_ENDPOINT_URL`,
  `NVOI_BUCKET_REGION`, `NVOI_BUCKET_ACCESS_KEY_ID` and `NVOI_BUCKET_SECRET_ACCESS_KEY` (bin/env), or the same names
  in the credentials. An installation may have none: building and deploying images then refuse (`ready!/0`).
  """

  @names ~w(nvoi_bucket nvoi_bucket_endpoint_url nvoi_bucket_access_key_id nvoi_bucket_secret_access_key)

  @doc "Refuses (501 `bucket_not_set_up`) unless the bucket and its keys are set."
  def ready! do
    if Enum.any?(@names, &(Nvoi.Credentials.setting(&1) == nil)),
      do:
        Nvoi.Tools.Action.refuse!(
          "This server has no bucket for images. Its admin sets NVOI_BUCKET, NVOI_BUCKET_ENDPOINT_URL, " <>
            "NVOI_BUCKET_ACCESS_KEY_ID and NVOI_BUCKET_SECRET_ACCESS_KEY: an S3-compatible bucket that build " <>
            "machines and cluster nodes reach from the internet.",
          "bucket_not_set_up",
          501
        )

    :ok
  end

  # How long a signed URL works: a large image's upload or download takes minutes.
  @seconds 1800

  @doc "A URL that does `method` (`GET`, `PUT`, `DELETE`) on the object `key` for #{@seconds} seconds from `at`."
  def sign(method, key, at \\ DateTime.utc_now()) do
    config = config!()
    uri = URI.parse(config.endpoint)
    path = "/#{config.bucket}/" <> Enum.map_join(String.split(key, "/"), "/", &encode/1)
    host = if uri.port in [80, 443, nil], do: uri.host, else: "#{uri.host}:#{uri.port}"
    "#{uri.scheme}://#{host}#{path}?" <> query(config, method, host, path, at, @seconds)
  end

  @doc false
  # The signed query of a request on `host` and `path` (Signature Version 4's presigned form).
  def query(config, method, host, path, at, seconds) do
    stamp = Calendar.strftime(at, "%Y%m%dT%H%M%SZ")
    day = Calendar.strftime(at, "%Y%m%d")
    scope = "#{day}/#{config.region}/s3/aws4_request"

    query =
      [
        {"X-Amz-Algorithm", "AWS4-HMAC-SHA256"},
        {"X-Amz-Credential", "#{config.key_id}/#{scope}"},
        {"X-Amz-Date", stamp},
        {"X-Amz-Expires", to_string(seconds)},
        {"X-Amz-SignedHeaders", "host"}
      ]
      |> Enum.sort()
      |> Enum.map_join("&", fn {k, v} -> "#{encode(k)}=#{encode(v)}" end)

    canonical =
      Enum.join([method, path, query, "host:#{host}\n", "host", "UNSIGNED-PAYLOAD"], "\n")

    to_sign =
      Enum.join(["AWS4-HMAC-SHA256", stamp, scope, hex(:crypto.hash(:sha256, canonical))], "\n")

    signing =
      Enum.reduce(
        [day, config.region, "s3", "aws4_request"],
        "AWS4" <> config.secret,
        &hmac(&2, &1)
      )

    "#{query}&X-Amz-Signature=#{hex(hmac(signing, to_sign))}"
  end

  @doc "The object `key` removed from the bucket; one already gone is fine."
  def delete!(key) do
    case Req.delete(
           sign("DELETE", key),
           [retry: false] ++ Application.get_env(:nvoi, :bucket_req, [])
         ) do
      {:ok, %{status: status}} when status in 200..299 or status == 404 ->
        :ok

      {:ok, %{status: status}} ->
        raise Compute.Error, message: "the bucket refused to delete #{key}: #{status}"

      {:error, e} ->
        raise Compute.Error, message: "the bucket did not answer: #{Exception.message(e)}"
    end
  end

  defp config! do
    get = fn name ->
      Nvoi.Credentials.setting(name) ||
        raise Compute.Error, message: "#{String.upcase(name)} is not set"
    end

    %{
      bucket: get.("nvoi_bucket"),
      endpoint: get.("nvoi_bucket_endpoint_url"),
      region: Nvoi.Credentials.setting("nvoi_bucket_region") || "auto",
      key_id: get.("nvoi_bucket_access_key_id"),
      secret: get.("nvoi_bucket_secret_access_key")
    }
  end

  defp encode(text), do: URI.encode(text, &URI.char_unreserved?/1)
  defp hmac(key, data), do: :crypto.mac(:hmac, :sha256, key, data)
  defp hex(bytes), do: Base.encode16(bytes, case: :lower)
end
