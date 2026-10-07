defmodule Nvoi.Source do
  @moduledoc """
  Where a repository lives, named by its `host`. The core's own source is the forge (`Nvoi.Source.Forge`, the
  repositories nvoi hosts), and GitHub's (`Nvoi.Source.GitHub`) when GitHub is on (Nvoi.GitHub.enabled?/0). Every question
  about a repository's git goes to its source: where git reaches it and as whom, its files, who holds it, a pull
  request, a ship.
  """

  @doc "Where git clones and pushes it."
  @callback clone_url(repo :: struct) :: String.t()
  @doc "As whom git reaches it, narrowed to `permissions` where the source can: `{user, password}`."
  @callback credentials(repo :: struct, permissions :: map) :: {String.t(), String.t()}
  @doc "The origin a checkout names, a label only: the machine never fetches."
  @callback origin_url(repo :: struct) :: String.t()
  @doc "Its files as the manifest reads them: the root's names and one file, at a ref."
  @callback files(repo :: struct) :: %{root: function, file: function}
  @doc "Who holds it, for the names of the machines made from it."
  @callback owner(repo :: struct) :: String.t()
  @doc "Opens a pull request from the worktree's branch onto its base: the agent's answer, or raises Nvoi.Tools.Error."
  @callback open_pull_request(
              repo :: struct,
              worktree :: struct,
              title :: String.t(),
              body :: String.t()
            ) ::
              String.t()
  @doc "Merges the worktree's branch into its base (Ship): the commit, or why not."
  @callback merge(repo :: struct, worktree :: struct, message :: String.t()) ::
              {:ok, String.t()}
              | {:refused, code :: String.t(), status :: integer, message :: String.t()}

  @doc "The repository's source; a GitHub repository's refuses while GitHub is off."
  def of(%{host: "forge"}), do: Nvoi.Source.Forge

  def of(%{host: "github"}) do
    Nvoi.GitHub.enabled!()
    Nvoi.Source.GitHub
  end

  def of(%{host: host}),
    do: raise(ArgumentError, "this installation has no source for #{host} repositories")
end
