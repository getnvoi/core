defmodule Compute.NeonLiveTest do
  @moduledoc """
  A real Neon project, as a production environment's database is made: created, found again by its name with the same
  variables, its size read, then destroyed. Runs only with `NEON_TEST_API_KEY` (a key of a Neon account kept for
  tests): `NEON_TEST_API_KEY=… mix test test/compute/neon_live_test.exs`.
  """
  use ExUnit.Case, async: false
  alias Compute.Database.Neon

  @key System.get_env("NEON_TEST_API_KEY")
  @moduletag :live
  @moduletag timeout: 600_000
  if @key in [nil, ""], do: @moduletag(skip: "NEON_TEST_API_KEY is not set")

  test "made, found again by its name, destroyed" do
    name = "nvoi-test-#{System.unique_integer([:positive])}"
    limits = %{"compute" => 0.25, "storage" => 1.0}
    made = Neon.create(@key, name, "17", limits)

    try do
      assert %{ref: ref, env: env} = Neon.find(@key, name)
      assert ref == made.ref
      assert env["PGPASSWORD"] == made.env["PGPASSWORD"]
      assert env["PGHOST"] == made.env["PGHOST"]
      assert is_integer(Neon.size(@key, ref))
    after
      Neon.destroy(@key, made.ref)
    end

    assert Neon.find(@key, name) == nil
  end
end
