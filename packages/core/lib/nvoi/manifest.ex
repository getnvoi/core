defmodule Nvoi.Manifest do
  @moduledoc """
  A runtime as data: the files that identify it, what git ignores, its variables, the setup commands and the
  services, `app` the one the preview shows. From the repository's `nvoi.yml`, the catalog (`priv/examples/*/nvoi.yml`), or
  the commands typed on an environment.
  """
  @file_name "nvoi.yml"
  @lock "${TMPDIR:-/tmp}/nvoi-before.lock"
  @reserved_ports [8080]
  @before_tries 30

  defmodule Invalid do
    defexception [:message]
  end

  defstruct [
    :runtime,
    :source,
    :digest,
    :port,
    typed: false,
    detect: [],
    ignore: [],
    tools: [],
    setup: [],
    env: %{},
    services: [],
    names: []
  ]

  def file_name, do: @file_name

  def parse(text, source, runtime \\ nil) do
    doc =
      case YamlElixir.read_from_string(to_string(text)) do
        {:ok, doc} when is_map(doc) -> doc
        {:ok, _} -> raise Invalid, message: "#{source}: not a mapping"
        {:error, e} -> raise Invalid, message: "#{source}: #{Exception.message(e)}"
      end

    build(
      doc,
      source,
      runtime,
      :crypto.hash(:sha256, to_string(text)) |> Base.encode16(case: :lower) |> binary_part(0, 16)
    )
  end

  def catalog do
    case :persistent_term.get(__MODULE__, nil) do
      nil ->
        catalog =
          Path.join(:code.priv_dir(:nvoi), "examples/*/#{@file_name}")
          |> Path.wildcard()
          |> Enum.map(
            &parse(
              File.read!(&1),
              "examples/#{Path.basename(Path.dirname(&1))}/#{@file_name}",
              Path.basename(Path.dirname(&1))
            )
          )
          |> Enum.sort_by(&{length(&1.detect), &1.runtime})

        :persistent_term.put(__MODULE__, catalog)
        catalog

      catalog ->
        catalog
    end
  end

  def runtime(name),
    do:
      Enum.find(catalog(), &(&1.runtime == name)) || raise(Invalid, message: "no runtime #{name}")

  def detect(files), do: Enum.find(catalog(), fn m -> Enum.any?(m.detect, &(&1 in files)) end)
  def app(manifest), do: Enum.find(manifest.services, &(&1.name == "app"))
  def port(%{typed: true, port: port}), do: port
  def port(manifest), do: app(manifest).port

  defp build(doc, source, runtime, digest, typed \\ false) do
    env = Map.get(doc, "env") || %{}
    if not is_map(env), do: raise(Invalid, message: "#{source}: env is not a mapping")

    manifest = %__MODULE__{
      source: source,
      runtime: runtime,
      digest: digest,
      typed: typed,
      detect: strings(doc, "detect", source),
      ignore: strings(doc, "ignore", source),
      tools: strings(doc, "tools", source),
      setup: strings(doc, "setup", source),
      env: Map.new(env, fn {k, v} -> {to_string(k), to_string(v)} end),
      services: doc |> Map.get("services") |> List.wrap() |> Enum.map(&service(&1, source))
    }

    names = Enum.map(manifest.services, & &1.name)
    if Enum.uniq(names) != names, do: raise(Invalid, message: "#{source}: service names repeat")

    if not typed and (app(manifest) == nil or app(manifest).port == nil),
      do: raise(Invalid, message: "#{source}: no service named app with a port")

    manifest
  end

  @doc """
  The commands typed on an environment, as a manifest. They replace the matched runtime's setup and services, never
  its toolchain (`tools`, such as Bun through mise): a typed `bun install` still finds bun.
  """
  def typed(environment) do
    commands = Nvoi.EnvironmentCommand.listed(environment.id)
    setup = Enum.filter(commands, &(&1.kind == "setup"))

    services =
      for c <- commands,
          c.kind == "preview",
          do: %{"name" => c.name, "command" => c.command, "port" => c.port, "before" => c.before}

    %{
      build(
        %{"setup" => Enum.map(setup, & &1.command), "services" => services},
        "environment #{environment.name}",
        nil,
        nil,
        true
      )
      | port: environment.preview_port,
        names: Enum.map(setup, & &1.name),
        tools: runtime_tools(environment.manifest)
    }
  end

  defp runtime_tools(label) when label in [nil, @file_name], do: []
  defp runtime_tools(label), do: runtime(label).tools

  defp strings(doc, key, source) do
    value = doc |> Map.get(key) |> List.wrap()

    if Enum.all?(value, &is_binary/1),
      do: value,
      else: raise(Invalid, message: "#{source}: #{key} must be a list of strings")
  end

  defp service(row, source) when is_map(row) do
    name = row["name"]
    command = row["command"]

    if Nvoi.Text.blank?(name) or Nvoi.Text.blank?(command),
      do: raise(Invalid, message: "#{source}: a service needs a name and a command")

    port = row["port"]

    if not (is_nil(port) or (is_integer(port) and port in 1..65_535)),
      do: raise(Invalid, message: "#{source}: #{name} has port #{inspect(port)}")

    if port in @reserved_ports,
      do:
        raise(Invalid,
          message: "#{source}: #{name} takes port #{port}, which the preview proxy listens on"
        )

    before = List.wrap(row["before"])

    if not Enum.all?(before, &is_binary/1),
      do: raise(Invalid, message: "#{source}: #{name} before must be a list of strings")

    %{name: to_string(name), command: to_string(command), before: before, port: port}
  end

  defp service(_, source), do: raise(Invalid, message: "#{source}: a service is not a mapping")

  defp exports(manifest),
    do:
      Enum.map(manifest.env, fn {k, v} ->
        ~s(export #{k}="#{String.replace(v, ~r/["\\\\`]/, "\\\\\\0")}")
      end)

  def setup_commands(manifest) do
    tools =
      manifest.tools
      |> Enum.with_index(1)
      |> Enum.map(fn {command, i} -> %{name: "tools-#{i}", command: command} end)

    setup =
      manifest.setup
      |> Enum.with_index()
      |> Enum.map(fn {command, i} ->
        %{name: Enum.at(manifest.names, i) || "setup-#{i + 1}", command: command}
      end)

    for c <- tools ++ setup, do: %{c | command: Enum.join(exports(manifest) ++ [c.command], "\n")}
  end

  @doc "Each service waits on the ports of those before it; its `before` commands run under a lock, retried."
  def service_commands(manifest) do
    manifest.services
    |> Enum.with_index()
    |> Enum.map(fn {service, i} ->
      waits =
        for s <- Enum.take(manifest.services, i),
            s.port,
            do: "until (echo > /dev/tcp/127.0.0.1/#{s.port}) 2>/dev/null; do sleep 1; done"

      before =
        if service.before != [],
          do:
            ["exec 9>#{@lock}", "! command -v flock >/dev/null || flock 9"] ++
              retried(service.before) ++ ["exec 9>&-"],
          else: []

      port = service.port && "export PORT=#{service.port}"

      %{
        name: service.name,
        command:
          (["set -e"] ++ exports(manifest) ++ [port] ++ waits ++ before ++ [service.command])
          |> Enum.reject(&is_nil/1)
          |> Enum.join("\n")
      }
    end)
  end

  defp retried(commands) do
    ["for try in $(seq 1 #{@before_tries}); do", "  if ( set -e"] ++
      Enum.map(commands, &"    #{&1}") ++
      [
        "  ); then break; fi",
        "  [ \"$try\" -lt #{@before_tries} ] || exit 1",
        "  sleep 2",
        "done"
      ]
  end
end
