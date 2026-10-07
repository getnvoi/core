defmodule NvoiWeb.SignupGitHubTest do
  # Signing up through GitHub takes the same lock as any other sign-up.
  # Every new account waits on approval, whatever made it, and nvoi approves it by hand (Nvoi.User.approve!/1).
  # Until then no token is made for it, and a token it held stops working at every door.
  use NvoiWeb.ConnCase, async: false
  setup {Nvoi.GitHubFixtures, :on}
  alias Nvoi.Postgres

  defp signup(body), do: build_conn() |> post("/api/v1/signup", body)

  defp email(kind), do: "#{kind}#{System.unique_integer([:positive])}@etudes-studio.com"

  test "GitHub takes the same path: whatever account it lands in, no token until approved" do
    email = email("claimed")

    signup(%{email: email, password: "a long password", workspace: "Claimed"})
    |> json_response(201)

    {owner, false} =
      Nvoi.GitHubSignIn.account(%{id: System.unique_integer([:positive]), login: "owner"}, [
        email
      ])

    assert Nvoi.User.pending?(owner)

    {fresh, true} =
      Nvoi.GitHubSignIn.account(%{id: System.unique_integer([:positive]), login: "new"}, [
        email("github")
      ])

    assert Nvoi.User.pending?(fresh)
    session = Postgres.insert!(%Nvoi.Session{user_id: fresh.id})
    assert_raise Nvoi.User.Pending, fn -> Nvoi.Session.token(session) end
  end
end
