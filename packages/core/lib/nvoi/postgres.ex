defmodule Nvoi.Postgres do
  use Ecto.Repo, otp_app: :nvoi, adapter: Ecto.Adapters.Postgres

  @doc """
  The database's URLs in production, read at boot (config/runtime.exs) from `env`: `url`, through a pooler or not,
  and `direct`, Postgres itself, for what needs one session for its whole life. `url` is `DATABASE_URL`; without it,
  `DATABASE_HOST` with the `postgres` user, its password the `postgres_password` secret (Nvoi.Credentials) and the
  database `nvoi`, as the self-hosted compose.yml runs it. `direct` is `DIRECT_DATABASE_URL`, else `url`.
  """
  def urls(env \\ System.get_env()) do
    url =
      Nvoi.Text.presence(env["DATABASE_URL"]) ||
        case Nvoi.Text.presence(env["DATABASE_HOST"]) do
          nil ->
            raise "DATABASE_URL is not set (ecto://USER:PASS@HOST/DATABASE), nor DATABASE_HOST"

          host ->
            password = URI.encode_www_form(Nvoi.Credentials.fetch!(["postgres_password"]))
            "ecto://postgres:#{password}@#{host}/nvoi"
        end

    %{url: url, direct: Nvoi.Text.presence(env["DIRECT_DATABASE_URL"]) || url}
  end
end
