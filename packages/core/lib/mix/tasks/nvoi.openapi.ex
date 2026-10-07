defmodule Mix.Tasks.Nvoi.Openapi do
  @shortdoc "Writes the API document from the tools' declarations"
  @moduledoc """
  `mix nvoi.openapi` writes the project's `priv/openapi.yaml` from the tools its router serves
  (`Nvoi.Tools.Document`): the core's in packages/core, nvoi.to's (the core's and its own) in packages/saas. A
  project that names `config :nvoi, :cli_document` writes the CLI's copy there too: one CLI for every server, from
  the document that has every operation. `mix nvoi.openapi --check` fails when a committed copy differs.
  """
  use Mix.Task

  def run(args) do
    Mix.Task.run("compile")
    document = Nvoi.Tools.Document.build()
    yaml = Nvoi.Tools.Document.yaml(document)
    cli = Application.get_env(:nvoi, :cli_document)
    json = cli && Jason.encode_to_iodata!(Nvoi.Tools.Document.sorted(document), pretty: true)

    if "--check" in args do
      if File.read!("priv/openapi.yaml") != yaml,
        do: Mix.raise("priv/openapi.yaml is not what the declarations give: run mix nvoi.openapi")

      if cli && File.read!(cli) != IO.iodata_to_binary(json),
        do: Mix.raise("#{cli} is not what the declarations give: run mix nvoi.openapi")
    else
      File.mkdir_p!("priv")
      File.write!("priv/openapi.yaml", yaml)
      if cli, do: File.write!(cli, json)
      Mix.shell().info("wrote priv/openapi.yaml" <> if(cli, do: " and #{cli}", else: ""))
    end
  end
end
