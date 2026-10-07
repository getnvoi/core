defmodule Nvoi.StepsTest do
  use ExUnit.Case, async: true

  # boxd answering every command with exit 0, sending each request's body to the test.
  defmodule Boxd do
    def init(test), do: test

    def call(conn, test) do
      {:ok, body, conn} = Plug.Conn.read_body(conn)
      send(test, {:command, Jason.decode!(body)})

      conn
      |> Plug.Conn.put_resp_content_type("application/json")
      |> Plug.Conn.send_resp(200, Jason.encode!(%{id: "c1", exit_code: 0, output: ""}))
    end
  end

  test "every start makes the scratch folder again, writable by every user, as root" do
    server = start_supervised!({Bandit, plug: {Boxd, self()}, port: 0, ip: :loopback})
    {:ok, {_, port}} = ThousandIsland.listener_info(server)
    box = Compute.Box.new(%{scheme: "http", host: "127.0.0.1:#{port}", headers: %{}}, "token")

    Nvoi.Steps.scratch!(box)

    assert_received {:command, %{"argv" => ["bash", "-lc", script]} = command}
    assert script == "mkdir -p /tmp/nvoi && chmod 1777 /tmp/nvoi"
    refute Map.has_key?(command, "as")
  end
end
