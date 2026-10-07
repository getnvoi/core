defmodule Nvoi.FakeSandbox do
  @moduledoc """
  A build machine for tests (`config :nvoi, :sandbox`): files put on it and scripts run are sent to the test process
  (`{:sandbox, :put, path, bytes}`, `{:sandbox, :run, script}`, `{:sandbox, :park, name}`); a script is answered by
  `answer/1`'s function (lines and an exit code), else a build prints two lines and the upload says its sha256 and
  size. The images it built are kept in `built/0` for `Compute.FakeSsh` answers.
  """
  @behaviour Nvoi.Builder.Sandbox

  def use do
    Application.put_env(:nvoi, :sandbox, __MODULE__)
    Process.put({__MODULE__, :built}, [])
    ExUnit.Callbacks.on_exit(fn -> Application.delete_env(:nvoi, :sandbox) end)
    :ok
  end

  def answer(fun), do: Process.put({__MODULE__, :answer}, fun)
  def built, do: Process.get({__MODULE__, :built}, [])

  @impl true
  def up!(builder, say) do
    say.("machine", "#{builder.name} is up")
    builder.name
  end

  @impl true
  def put!(_machine, path, bytes) do
    send(self(), {:sandbox, :put, path, bytes})
    :ok
  end

  @impl true
  def run(_machine, script, on_line) do
    send(self(), {:sandbox, :run, script})
    {lines, code} = (Process.get({__MODULE__, :answer}) || (&default/1)).(script)
    Enum.each(lines, on_line)
    code
  end

  @impl true
  def park!(builder) do
    send(self(), {:sandbox, :park, builder.name})
    :ok
  end

  def default(script) do
    cond do
      script =~ "docker build" ->
        [_, name] = Regex.run(~r/-t (\S+)/, script)
        Process.put({__MODULE__, :built}, [name | built()])
        {["#1 [internal] load build definition from Dockerfile", "#9 DONE 0.4s"], 0}

      script =~ "docker save" ->
        {[String.duplicate("ab", 32) <> " 4096"], 0}

      true ->
        {[], 0}
    end
  end
end
