defmodule Compute.BoxTest do
  use ExUnit.Case, async: true

  alias Compute.Box

  # boxd behind a proxy that answers 502 for the first `fails` requests of each method, then 204.
  defmodule Flaky do
    def init(opts), do: opts

    def call(conn, %{counter: counter, fails: fails}) do
      seen = :counters.get(counter, 1)
      :counters.add(counter, 1, 1)
      status = if seen < fails, do: 502, else: 204
      Plug.Conn.send_resp(conn, status, "")
    end
  end

  defp box(fails) do
    counter = :counters.new(1, [])

    server =
      start_supervised!(
        {Bandit, plug: {Flaky, %{counter: counter, fails: fails}}, port: 0, ip: :loopback}
      )

    {:ok, {_, port}} = ThousandIsland.listener_info(server)
    {Box.new(%{scheme: "http", host: "127.0.0.1:#{port}", headers: %{}}, "token"), counter}
  end

  test "a file written while the proxy loses the port for a moment is written once it answers again" do
    {box, counter} = box(2)
    assert Box.put(box, "/tmp/x", "hi") == ""
    assert :counters.get(counter, 1) == 3
  end

  test "a command is never started twice: a 502 on POST is the error" do
    {box, counter} = box(1)
    assert_raise Compute.Error, ~r/502/, fn -> Box.start(box, ["true"]) end
    assert :counters.get(counter, 1) == 1
  end
end
