defmodule Nvoi.CoauthorsTest do
  use ExUnit.Case, async: true

  test "a linked GitHub account is named by its noreply address, else by the sign-in email" do
    linked = %Nvoi.User{
      pending_approval: false,
      first_name: "Ben",
      last_name: "B",
      email: "b@x.to",
      github_user_id: 7,
      github_login: "bb"
    }

    assert Nvoi.User.coauthor(linked) == "Ben B <7+bb@users.noreply.github.com>"
    assert Nvoi.User.coauthor(%{linked | github_user_id: nil}) == "Ben B <b@x.to>"
  end

  test "no hooks, no environment" do
    assert Nvoi.Coauthors.env(nil, []) == %{}
    assert Nvoi.Coauthors.env("/h", [])["GIT_CONFIG_VALUE_0"] == "/h"
  end
end
