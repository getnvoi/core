defmodule Nvoi.Hosts do
  @moduledoc """
  The names an installation answers on, from the one it is given (`NVOI_HOST`, read at boot in production): the
  site, then git, MCP, the preview worker's questions and the previews each on a name of their own, beside it. A
  domain's apex (two labels) puts the part before it (`nvoi.to`: `git.nvoi.to`, `*-preview.nvoi.to`); a name
  under a domain puts the part beside it, so every name stays one label under the domain and its free certificate
  (`nvoi.acme.com`: `git-nvoi.acme.com`, `*-preview-nvoi.acme.com`). The CLI finds git and MCP the same way.
  """

  @doc "`[site_host:, git_host:, mcp_host:, sandbox_host:, preview_suffix:]` for `host`."
  def from(host) do
    beside =
      case String.split(host, ".") do
        [_, _] -> fn part -> "#{part}.#{host}" end
        [label | domain] -> fn part -> "#{part}-#{label}.#{Enum.join(domain, ".")}" end
      end

    [
      site_host: host,
      git_host: beside.("git"),
      mcp_host: beside.("mcp"),
      sandbox_host: beside.("sandbox"),
      preview_suffix: "-" <> beside.("preview")
    ]
  end

  @doc """
  Whether `name` is one of the installation's own: the site, git, MCP, the sandbox host, and a preview's name
  (`<slug><preview suffix>`, one label) while previews are on (`Nvoi.Previews`). The proxy in front asks this
  before it gets a certificate for a name (`GET /tls`).
  """
  def own?(name) do
    names =
      Enum.map(~w(site_host git_host mcp_host sandbox_host)a, &Application.get_env(:nvoi, &1))

    suffix = Application.get_env(:nvoi, :preview_suffix)

    name in names or
      (Nvoi.Previews.enabled?() and String.ends_with?(name, suffix) and
         Regex.match?(~r/\A[a-z0-9-]+\z/, String.replace_suffix(name, suffix, "")))
  end
end
