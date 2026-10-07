defmodule Nvoi.RunnerTest do
  use ExUnit.Case, async: true

  test "a run's first line writes the machine's activity, whatever the monotonic clock reads, then once a minute" do
    # The BEAM's monotonic clock starts far below zero.
    now = -576_460_752
    assert Nvoi.Runner.due?(nil, now)
    refute Nvoi.Runner.due?(now, now + 59)
    assert Nvoi.Runner.due?(now, now + 60)
  end
end
