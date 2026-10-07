defmodule Nvoi.Jobs.RescueTest do
  use Nvoi.DataCase, async: false
  alias Nvoi.Jobs.Rescue

  # A job some node is executing since `ago` seconds.
  defp executing(node, ago) do
    at = DateTime.add(DateTime.utc_now(), -ago)
    job = Postgres.insert!(Nvoi.Jobs.PricesJob.new(%{}))

    Postgres.update_all(from(j in "oban_jobs", where: j.id == ^job.id),
      set: [state: "executing", attempted_by: [node, "default", "x"], attempted_at: at]
    )

    job.id
  end

  defp state(id),
    do: Postgres.one!(from j in "oban_jobs", where: j.id == ^id, select: j.state)

  test "a job running for hours on a live node is never put back" do
    slow = executing(to_string(node()), 2 * 3600)
    Rescue.orphans()
    assert state(slow) == "executing"
  end

  test "a gone node's jobs are put back at once, another node's are not" do
    gone = executing("nvoi@10.0.0.99", 5)
    alive = executing(to_string(node()), 5)
    Rescue.gone(:"nvoi@10.0.0.99")
    assert state(gone) == "available"
    assert state(alive) == "executing"
  end

  test "the safety net puts back a job on no live node, after its first minute" do
    old = executing("nvoi@10.0.0.98", 120)
    fresh = executing("nvoi@10.0.0.97", 10)
    Rescue.orphans()
    assert state(old) == "available"
    assert state(fresh) == "executing"
  end
end
