defmodule Nvoi.Storage.BucketTest do
  use ExUnit.Case, async: false

  test "a presigned URL is AWS's own example, signature for signature" do
    # https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-query-string-auth.html
    config = %{
      region: "us-east-1",
      key_id: "AKIAIOSFODNN7EXAMPLE",
      secret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
    }

    query =
      Nvoi.Storage.Bucket.query(
        config,
        "GET",
        "examplebucket.s3.amazonaws.com",
        "/test.txt",
        ~U[2013-05-24 00:00:00Z],
        86_400
      )

    assert query =~
             "X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404"
  end

  test "a URL names one object of the bucket, for a while" do
    for {k, v} <- [
          {"NVOI_BUCKET", "nvoi-test"},
          {"NVOI_BUCKET_ENDPOINT_URL", "https://account.r2.cloudflarestorage.com"},
          {"NVOI_BUCKET_ACCESS_KEY_ID", "id"},
          {"NVOI_BUCKET_SECRET_ACCESS_KEY", "secret"}
        ],
        do: System.put_env(k, v)

    on_exit(fn ->
      for k <-
            ~w(NVOI_BUCKET NVOI_BUCKET_ENDPOINT_URL NVOI_BUCKET_ACCESS_KEY_ID NVOI_BUCKET_SECRET_ACCESS_KEY),
          do: System.delete_env(k)
    end)

    url = Nvoi.Storage.Bucket.sign("PUT", "images/w/r/abc.tar.gz")

    assert url =~
             ~r|\Ahttps://account\.r2\.cloudflarestorage\.com/nvoi-test/images/w/r/abc\.tar\.gz\?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Credential=id%2F\d{8}%2Fauto%2Fs3%2Faws4_request&|

    assert url =~ "X-Amz-Expires=1800"
    refute url =~ "secret"
  end

  test "an installation without a bucket refuses images plainly, with what to set" do
    e = assert_raise Nvoi.Tools.Action.Refused, &Nvoi.Storage.Bucket.ready!/0
    assert {e.status, e.code} == {501, "bucket_not_set_up"}
    assert e.message =~ "NVOI_BUCKET_ENDPOINT_URL"
  end
end
