defmodule Nvoi.BoxdCurrentTest do
  # A machine's boxd is made the release's only when nothing runs on it: a restarted boxd takes the commands it finds
  # as ended, which would end an agent's turn. The old shell server is stopped before the upgrade.
  use ExUnit.Case, async: false
  alias Compute.Box

  # boxd as the test says it is: its binary's sum and its live commands; every request reaches the test.
  defmodule Boxd do
    def init(state), do: state

    def call(conn, {test, sum, live}) do
      {:ok, body, conn} = Plug.Conn.read_body(conn)
      send(test, {conn.method, conn.request_path, body})

      answer =
        case {conn.method, conn.request_path} do
          {"GET", "/commands"} ->
            %{commands: live}

          {"POST", "/commands"} ->
            %{id: "c1", exit_code: 0, output: "#{sum}  /usr/local/bin/boxd\n"}

          _ ->
            %{}
        end

      conn
      |> Plug.Conn.put_resp_content_type("application/json")
      |> Plug.Conn.send_resp(200, Jason.encode!(answer))
    end
  end

  setup do
    binary = Application.get_env(:nvoi, :boxd_binary)
    path = Path.join(System.tmp_dir!(), "boxd-#{System.unique_integer([:positive])}")
    File.write!(path, "the release's boxd")
    Application.put_env(:nvoi, :boxd_binary, path)

    on_exit(fn ->
      if binary,
        do: Application.put_env(:nvoi, :boxd_binary, binary),
        else: Application.delete_env(:nvoi, :boxd_binary)

      File.rm(path)
    end)

    %{sum: :crypto.hash(:sha256, "the release's boxd") |> Base.encode16(case: :lower)}
  end

  defp box(sum, live) do
    server =
      start_supervised!({Bandit, plug: {Boxd, {self(), sum, live}}, port: 0, ip: :loopback},
        id: make_ref()
      )

    {:ok, {_, port}} = ThousandIsland.listener_info(server)
    Box.new(%{scheme: "http", host: "127.0.0.1:#{port}", headers: %{}}, "t")
  end

  defp sent(acc \\ []) do
    receive do
      {method, path, body} -> sent([{method, path, body} | acc])
    after
      0 -> Enum.reverse(acc)
    end
  end

  test "the release's boxd is left as it is", ctx do
    assert Nvoi.Boxd.current!(box(ctx.sum, [])) == :current
    refute Enum.any?(sent(), &match?({"PUT", "/file", _}, &1))
  end

  test "an older boxd on a busy machine is kept: its turn would end", %{} do
    live = [%{id: "t1", name: "turn-abc"}, %{id: "s1", name: "terminal"}]
    assert Nvoi.Boxd.current!(box("older", live)) == :busy
    calls = sent()
    refute Enum.any?(calls, &match?({"PUT", "/file", _}, &1))
    refute Enum.any?(calls, &match?({"DELETE", "/commands/s1", _}, &1))
  end

  test "an older boxd on an idle machine is replaced, its old shell server stopped first", %{} do
    assert Nvoi.Boxd.current!(box("older", [%{id: "s1", name: "terminal"}])) == :upgraded
    calls = sent()
    paths = for {method, path, _} <- calls, do: {method, path}
    kill = Enum.find_index(paths, &(&1 == {"DELETE", "/commands/s1"}))
    put = Enum.find_index(paths, &(&1 == {"PUT", "/file"}))
    assert kill < put
    assert Enum.any?(calls, fn {_, _, body} -> body =~ "systemctl restart nvoi-boxd" end)
  end
end
