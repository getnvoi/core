defmodule Compute.DaytonaTest do
  use ExUnit.Case, async: true

  alias Compute.SandboxVm.Daytona

  test "every page of the account's sandboxes is read, following nextCursor until there is none" do
    pages = %{
      nil => %{"items" => [%{"id" => "a"}, %{"id" => "b"}], "nextCursor" => "c2"},
      "c2" => %{"items" => [%{"id" => "c"}], "nextCursor" => "c3"},
      "c3" => %{"items" => [%{"id" => "d"}]}
    }

    assert Daytona.pages(&pages[&1]) |> Enum.map(& &1["id"]) == ~w(a b c d)
  end

  test "an answer that is a plain list is the whole list" do
    assert Daytona.pages(fn nil -> [%{"id" => "a"}] end) == [%{"id" => "a"}]
  end
end
