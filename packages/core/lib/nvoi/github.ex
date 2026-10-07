defmodule Nvoi.GitHub do
  @moduledoc """
  GitHub's REST API with one token: an App installation's, narrowed per call, or a person's. GitHub is optional: it
  is on when the App's credentials resolve (Nvoi.GitHubApp), and only then does the core offer its source, its
  connector, its onboarding step and its MCP sign-in. Its routes and tools are always declared; with GitHub off they
  answer `off/0`.
  """
  @base "https://api.github.com"
  @per_page 100
  @pages 50

  defmodule Error do
    defexception [:message, :status]
  end

  @doc "Whether GitHub is set up: every key of the App's credentials resolves."
  def enabled?, do: Nvoi.Credentials.missing(Nvoi.GitHubApp.paths()) == []

  @doc "The refusal of a GitHub route or tool while GitHub is off."
  def off,
    do: %Nvoi.Tools.Action.Refused{
      message:
        "GitHub is not set up on this server. Its admin turns it on with the credentials of a GitHub App.",
      code: "github_not_set_up",
      status: 501
    }

  @doc "Raises `off/0` unless GitHub is on."
  def enabled! do
    if not enabled?(), do: raise(off())
    :ok
  end

  def new(token, base \\ @base), do: %{token: token, base: base}

  @doc "An installation token narrowed to `repositories` and `permissions`."
  def installation_token(installation_id, repositories \\ nil, permissions \\ nil) do
    body =
      %{repositories: repositories, permissions: permissions}
      |> Map.reject(fn {_, v} -> is_nil(v) end)

    Nvoi.GitHubApp.jwt()
    |> new()
    |> post("/app/installations/#{installation_id}/access_tokens", body)
    |> Map.fetch!("token")
  end

  @doc """
  Trades an OAuth code for a user token, with the PKCE verifier when the authorize URL carried its challenge. The body
  is never quoted in an error: it would hold the token.
  """
  def exchange_code(code, verifier \\ nil) do
    body =
      %{
        client_id: Nvoi.GitHubApp.client_id(),
        client_secret: Nvoi.GitHubApp.client_secret(),
        code: code,
        code_verifier: verifier
      }
      |> Map.reject(fn {_, v} -> is_nil(v) end)

    case new(nil, "https://github.com")
         |> request(:post, "/login/oauth/access_token", json: body, accept: "application/json") do
      %{"access_token" => token} when is_binary(token) ->
        token

      %{"error" => error} ->
        raise Error, message: "GitHub refused the code: #{error}"

      _ ->
        raise Error, message: "GitHub refused the code: no token"
    end
  end

  @doc "The token's account: %{login, type, id}."
  def user(client) do
    body = get(client, "/user")
    %{login: body["login"], type: body["type"], id: body["id"]}
  end

  @doc "The token's addresses GitHub has verified, the primary one first, downcased."
  def verified_emails(client) do
    client
    |> get("/user/emails")
    |> Enum.filter(& &1["verified"])
    |> Enum.sort_by(&if(&1["primary"], do: 0, else: 1))
    |> Enum.map(&String.downcase(&1["email"]))
  end

  @doc "Every installation of the App a user token can see."
  def user_installations(client),
    do: client |> get("/user/installations?per_page=#{@per_page}") |> Map.fetch!("installations")

  def repository(client, full_name) do
    case get(client, "/repos/#{full_name}", :missing) do
      nil -> nil
      body -> row(body)
    end
  end

  def installation_repositories(client),
    do: walk(client, "/installation/repositories?per_page=#{@per_page}", & &1["repositories"])

  def repositories(client) do
    user = get(client, "/user")
    orgs = client |> get("/user/orgs?per_page=#{@per_page}") |> Enum.map(& &1["login"])
    kept = MapSet.new([user["login"] | orgs], &String.downcase/1)

    client
    |> walk(
      "/user/repos?per_page=#{@per_page}&affiliation=owner,collaborator,organization_member",
      & &1
    )
    |> Enum.filter(
      &MapSet.member?(kept, &1.full_name |> String.split("/") |> hd() |> String.downcase())
    )
  end

  def root(client, full_name, ref),
    do:
      client
      |> get("/repos/#{full_name}/contents?ref=#{URI.encode_www_form(ref)}")
      |> Enum.map(& &1["name"])

  def file(client, full_name, path, ref) do
    case get(
           client,
           "/repos/#{full_name}/contents/#{path}?ref=#{URI.encode_www_form(ref)}",
           :missing
         ) do
      nil -> nil
      body -> body["content"] |> String.replace("\n", "") |> Base.decode64!()
    end
  end

  def post(client, path, body), do: request(client, :post, path, json: body)
  def get(client, path, missing \\ :raise), do: request(client, :get, path, missing: missing)

  defp walk(client, path, pick, page \\ 1, rows \\ []) do
    response = raw(client, :get, path, [])
    rows = rows ++ Enum.map(pick.(response.body), &row/1)

    case {next_link(response), page < @pages} do
      {nil, _} -> rows
      {_, false} -> rows
      {next, true} -> walk(client, next, pick, page + 1, rows)
    end
  end

  defp row(body) do
    %{
      github_id: body["id"],
      full_name: body["full_name"],
      private: body["private"],
      default_branch: body["default_branch"],
      pushed_at:
        body["pushed_at"] && match?({:ok, _, _}, DateTime.from_iso8601(body["pushed_at"])) &&
          elem(DateTime.from_iso8601(body["pushed_at"]), 1)
    }
  end

  defp request(client, method, path, opts) do
    {missing, opts} = Keyword.pop(opts, :missing, :raise)
    {accept, opts} = Keyword.pop(opts, :accept)
    response = raw(client, method, path, opts, missing, accept)
    response && response.body
  end

  defp raw(client, method, path, opts, missing \\ :raise, accept \\ nil) do
    url = if String.starts_with?(path, "http"), do: path, else: client.base <> path

    if URI.parse(url).host != URI.parse(client.base).host,
      do: raise(Error, message: "refused to send a GitHub token to #{URI.parse(url).host}")

    headers =
      [
        {"accept", accept || "application/vnd.github+json"},
        {"x-github-api-version", "2022-11-28"},
        {"user-agent", "nvoi"}
      ] ++
        if(client.token, do: [{"authorization", "Bearer #{client.token}"}], else: [])

    case Req.request(
           [method: method, url: url, headers: headers, retry: false, receive_timeout: 30_000] ++
             Application.get_env(:nvoi, :github_req, []) ++ opts
         ) do
      {:ok, %{status: status} = response} when status in 200..299 -> response
      {:ok, %{status: 404}} when missing != :raise -> nil
      {:ok, response} -> raise Error, message: describe(response), status: response.status
      {:error, e} -> raise Error, message: "GitHub could not be reached: #{Exception.message(e)}"
    end
  end

  defp describe(%{body: %{"message" => message}}) when message != "", do: message
  defp describe(response), do: "GitHub answered #{response.status}"

  defp next_link(response) do
    response
    |> Req.Response.get_header("link")
    |> List.first("")
    |> String.split(",")
    |> Enum.find_value(
      &(Regex.run(~r/<([^>]+)>\s*;\s*rel="next"/, &1, capture: :all_but_first)
        |> then(fn m -> m && hd(m) end))
    )
  end
end
