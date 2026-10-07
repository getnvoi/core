defmodule Nvoi.Tools.GitHubSignIn.Start do
  @moduledoc """
  Starts signing in with GitHub: the GitHub page to open in the browser, and the key the app keeps to collect the
  sign-in once GitHub sent the person back. The key is answered here only.
  """
  use Nvoi.Tools.Action,
    group: "github_sign_in",
    action: "start",
    category: :account,
    summary: "Start signing in with GitHub: the page to open and the key to collect with",
    post: "/github/sign_ins",
    status: 201,
    access: :sensitive,
    default_role: :open,
    # Answers before anyone is signed in.
    skip_authentication: true

  def call(_params, current) do
    Nvoi.GitHub.enabled!()
    {row, key, state} = Nvoi.GitHubSignIn.start!()

    {:created,
     %{
       github_sign_in: %{
         id: row.id,
         key: key,
         url: Nvoi.GitHubApp.sign_in_url(state, row.verifier, current.base_url)
       }
     }}
  end
end
