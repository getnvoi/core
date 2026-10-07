defmodule Nvoi.GitHubFixtures do
  @moduledoc """
  GitHub is on only with the App's credentials (Nvoi.GitHub.enabled?/0); the tests have none. These are stand-ins,
  not secrets: `on/1` puts them for one test module (`setup {Nvoi.GitHubFixtures, :on}`, `async: false`),
  `switch_on/0` for a whole run (the tests of an installation that has its App).
  """

  def app,
    do: %{
      "id" => "1",
      "client_id" => "Iv-test-client",
      "client_secret" => "test-client-secret",
      "private_key" => File.read!(Path.expand("../fixtures/github_app_test_key.pem", __DIR__)),
      "slug" => "nvoi-test",
      "webhook_secret" => "test-webhook-secret"
    }

  @doc "Puts the stand-ins beside the other credentials; answers the credentials before."
  def switch_on do
    before = Application.get_env(:nvoi, :credentials)

    Application.put_env(
      :nvoi,
      :credentials,
      Map.put(before, "github_app", app())
    )

    before
  end

  @doc "GitHub on for the test, off again after it."
  def on(_context) do
    before = switch_on()
    ExUnit.Callbacks.on_exit(fn -> Application.put_env(:nvoi, :credentials, before) end)
    :ok
  end
end
