defmodule Nvoi.GitHubSignIn do
  @moduledoc """
  Signing in with GitHub from the app, which has no browser session to keep the journey in. The app starts it and
  keeps the key it is given; the signed state carries the row to GitHub and back with a nonce spent once; the
  callback settles it (`signed_in` with the user and their workspace, or `refused` with why); the app then collects it
  once with its key and receives a new API token, which never appears in a URL. The PKCE verifier stays on the row.

  The key alone does not collect: the callback also hands the browser that approved on GitHub a one-time code (to the
  app through `nvoi://github?sign_in=…&code=…`, shown on the page too). Whoever started a sign-in and sent its link to
  someone else holds the key but never sees the code. Five wrong codes end the sign-in.
  """
  use Nvoi.Schema
  alias Nvoi.{GitHubApp, Postgres}

  schema "github_sign_ins" do
    field :key_digest, :binary
    field :code_digest, :binary
    field :code_misses, :integer, default: 0
    field :nonce_digest, :binary
    field :verifier, Nvoi.Encrypted
    field :state, :string, default: "pending"
    field :message, :string
    belongs_to :user, Nvoi.User
    belongs_to :workspace, Nvoi.Workspace
    field :settled_at, :utc_datetime_usec
    field :collected_at, :utc_datetime_usec
    timestamps()
  end

  @doc """
  A new sign-in: the row, the key the app keeps, and the state for GitHub. `connect` is an MCP client's
  authorization (NvoiWeb.Mcp.Authorize), which carries its signed request and needs no key.
  """
  def start!(purpose \\ "sign_in", request \\ nil) do
    key = Base.url_encode64(:crypto.strong_rand_bytes(32), padding: false)
    {nonce, nonce_digest} = GitHubApp.nonce()
    verifier = Base.url_encode64(:crypto.strong_rand_bytes(48), padding: false)

    row =
      Postgres.insert!(%__MODULE__{
        key_digest: GitHubApp.digest(key),
        nonce_digest: nonce_digest,
        verifier: verifier
      })

    {row, key, GitHubApp.sign_state(purpose, row.id, nonce, nil, nil, request)}
  end

  @doc "Whether the row is past the state's age: a sign-in nobody finished, or one nobody collected."
  def expired?(row) do
    since = row.settled_at || row.created_at
    DateTime.diff(DateTime.utc_now(), since) > GitHubApp.state_age()
  end

  @doc "Spends the state's nonce: true once, for a pending row."
  def spend(row, nonce) do
    row.state == "pending" and GitHubApp.nonce?(nonce, row.nonce_digest) and
      match?(
        {1, _},
        Postgres.update_all(
          from(s in __MODULE__,
            where: s.id == ^row.id and s.state == "pending" and not is_nil(s.nonce_digest)
          ),
          set: [nonce_digest: nil, updated_at: DateTime.utc_now()]
        )
      )
  end

  # Wrong codes a sign-in takes before it ends.
  @code_misses 5
  # Unambiguous letters and digits for a code a person may type.
  @alphabet ~c"ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

  @doc "Settles a sign-in as signed in with a new code, answered once: the row and the code."
  def signed_in!(row, attrs) do
    code =
      for(_ <- 1..8, into: "", do: <<Enum.random(@alphabet)>>)
      |> then(&(binary_part(&1, 0, 4) <> "-" <> binary_part(&1, 4, 4)))

    {settle!(row, "signed_in", Map.put(attrs, :code_digest, GitHubApp.digest(code))), code}
  end

  @doc "Whether `code` is the row's, as a person may type it (any case, with or without its dash), in constant time."
  def code?(%{code_digest: digest}, code) when is_binary(code) and is_binary(digest) do
    typed = code |> String.upcase() |> String.replace(~r/[^A-Z0-9]/, "")

    typed =
      if byte_size(typed) == 8,
        do: binary_part(typed, 0, 4) <> "-" <> binary_part(typed, 4, 4),
        else: typed

    Plug.Crypto.secure_compare(GitHubApp.digest(typed), digest)
  end

  def code?(_, _), do: false

  @doc "Counts a wrong code; past the limit the sign-in is refused. Answers the row."
  def missed!(row) do
    misses = row.code_misses + 1

    if misses >= @code_misses,
      do:
        settle!(row, "refused", %{
          code_misses: misses,
          message: "Too many wrong codes. Sign in with GitHub again."
        }),
      else: Postgres.update!(change(row, code_misses: misses))
  end

  def settle!(row, state, attrs \\ %{}) when state in ~w(signed_in refused) do
    Postgres.update!(
      change(row, Map.merge(attrs, %{state: state, settled_at: DateTime.utc_now()}))
    )
  end

  @doc "Whether `key` is the row's, in constant time."
  def key?(row, key) when is_binary(key),
    do: Plug.Crypto.secure_compare(GitHubApp.digest(key), row.key_digest)

  def key?(_, _), do: false

  @doc "Marks a signed-in row collected: true once, whoever races."
  def collect(row),
    do:
      match?(
        {1, _},
        Postgres.update_all(
          from(s in __MODULE__,
            where: s.id == ^row.id and s.state == "signed_in" and is_nil(s.collected_at)
          ),
          set: [collected_at: DateTime.utc_now(), updated_at: DateTime.utc_now()]
        )
      )

  @doc """
  The user a GitHub account signs in as: the one it is linked to, else the one owning one of its verified addresses,
  linked now; else a new one, with a workspace of their own. Whichever it is, it gets a token only once approved
  (`Nvoi.Session.token/1`). `{user, created}`, or nil when GitHub verified no address.
  """
  def account(identity, emails) do
    github = [github_user_id: identity.id, github_login: identity.login]

    cond do
      linked = Nvoi.Postgres.get_by(Nvoi.User, github_user_id: identity.id) ->
        {linked, false}

      owner =
          emails != [] &&
            Nvoi.Postgres.one(from u in Nvoi.User, where: u.email in ^emails, limit: 1) ->
        {Nvoi.Postgres.update!(change(owner, github)), false}

      emails != [] ->
        {:ok, user} =
          Nvoi.Postgres.transaction(fn ->
            user =
              Nvoi.Postgres.insert!(
                struct(
                  %Nvoi.User{
                    email: hd(emails),
                    password_digest:
                      Bcrypt.hash_pwd_salt(Base.url_encode64(:crypto.strong_rand_bytes(30)))
                  },
                  github
                )
              )

            local = hd(String.split(user.email, "@"))
            Nvoi.Workspace.open!(Nvoi.Text.presence(Nvoi.Text.parameterize(local)) || "me", user)
            user
          end)

        {user, true}

      true ->
        nil
    end
  end
end
