defmodule Nvoi.PostgresTest do
  # The database's URLs at boot: DATABASE_URL, else one built from DATABASE_HOST and the postgres_password secret;
  # DIRECT_DATABASE_URL, else the same URL.
  use ExUnit.Case, async: false

  test "DATABASE_URL and DIRECT_DATABASE_URL, each as given" do
    assert Nvoi.Postgres.urls(%{
             "DATABASE_URL" => "ecto://a@pooler/x",
             "DIRECT_DATABASE_URL" => "ecto://a@db/x"
           }) ==
             %{url: "ecto://a@pooler/x", direct: "ecto://a@db/x"}
  end

  test "the direct URL is the database's when it is not set" do
    assert Nvoi.Postgres.urls(%{"DATABASE_URL" => "ecto://a@db/x", "DIRECT_DATABASE_URL" => ""}) ==
             %{url: "ecto://a@db/x", direct: "ecto://a@db/x"}
  end

  test "without DATABASE_URL, the host and the postgres_password secret" do
    dir = Path.join(System.tmp_dir!(), "nvoi-pg-#{System.unique_integer([:positive])}")
    File.mkdir_p!(dir)
    File.write!(Path.join(dir, "postgres_password"), "p@ss/word\n")
    System.put_env("NVOI_SECRETS", dir)

    on_exit(fn ->
      System.delete_env("NVOI_SECRETS")
      File.rm_rf!(dir)
    end)

    assert Nvoi.Postgres.urls(%{"DATABASE_HOST" => "db"}) ==
             %{
               url: "ecto://postgres:p%40ss%2Fword@db/nvoi",
               direct: "ecto://postgres:p%40ss%2Fword@db/nvoi"
             }
  end

  test "neither names what to set" do
    assert_raise RuntimeError, ~r/DATABASE_URL is not set/, fn -> Nvoi.Postgres.urls(%{}) end
  end
end
