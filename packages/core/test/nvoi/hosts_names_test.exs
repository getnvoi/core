defmodule Nvoi.HostsNamesTest do
  # Every name an installation answers on comes from the one it is given.
  use ExUnit.Case, async: true

  test "an apex puts each part before it: nvoi.to keeps its names" do
    assert Nvoi.Hosts.from("nvoi.to") == [
             site_host: "nvoi.to",
             git_host: "git.nvoi.to",
             mcp_host: "mcp.nvoi.to",
             sandbox_host: "sandbox.nvoi.to",
             preview_suffix: "-preview.nvoi.to"
           ]
  end

  test "a name under a domain puts each part beside it, one label under the domain" do
    names = Nvoi.Hosts.from("nvoi.acme.com")
    assert names[:git_host] == "git-nvoi.acme.com"
    assert names[:mcp_host] == "mcp-nvoi.acme.com"
    assert names[:sandbox_host] == "sandbox-nvoi.acme.com"
    assert names[:preview_suffix] == "-preview-nvoi.acme.com"
  end
end
