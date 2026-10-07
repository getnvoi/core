defmodule Nvoi.SourceGitHubTest do
  # With GitHub on, its repositories have their source (off: GitHubOffTest).
  use ExUnit.Case, async: false
  setup {Nvoi.GitHubFixtures, :on}

  test "a GitHub repository's source is GitHub's" do
    assert Nvoi.Source.of(%Nvoi.Repo{host: "github"}) == Nvoi.Source.GitHub
  end
end
