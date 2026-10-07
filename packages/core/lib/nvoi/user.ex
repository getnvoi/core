defmodule Nvoi.User do
  use Nvoi.Schema

  schema "users" do
    field :email, :string

    # Locked until nvoi approves it by hand (approve!/1): every account starts so, whatever made it.
    field :pending_approval, :boolean, default: true
    field :github_login, :string
    field :github_user_id, :integer
    field :password_digest, :string
    field :first_name, :string
    field :last_name, :string
    has_many :sessions, Nvoi.Session
    has_many :llm_connections, Nvoi.LlmConnection
    has_many :workspace_users, Nvoi.WorkspaceUser
    timestamps()
  end

  @doc "How a person is shown to their team: their name, else their GitHub login, else the start of their address."
  def name(user) do
    case Enum.reject([user.first_name, user.last_name], &(&1 in [nil, ""])) do
      [] ->
        if user.github_login not in [nil, ""],
          do: user.github_login,
          else: user.email |> String.split("@") |> hd()

      parts ->
        Enum.join(parts, " ")
    end
  end

  @doc "Two letters for their avatar: the first of their first two words, else the first two of their name."
  def initials(user) do
    name = name(user)

    letters =
      case String.split(name, ~r/[\s._-]+/, trim: true) do
        [first, second | _] -> String.first(first) <> String.first(second)
        _ -> String.slice(name, 0, 2)
      end

    case String.upcase(letters) do
      "" -> "?"
      upcased -> upcased
    end
  end

  # Who may sign up, for now, by GitHub.
  @doc """
  Whether the account waits on approval. Every account starts so (the column and the schema default to true); a
  pending account never gets a token (`Nvoi.Session.token/1`) and a token it held stops working
  (`Nvoi.Session.resolve/1`), until nvoi approves it by hand.
  """
  def pending?(%{pending_approval: false}), do: false
  def pending?(_), do: true

  defmodule Pending do
    defexception message:
                   "Your account is pending approval: nvoi lets you in once it is approved."
  end

  @doc "What a pending account is told, wherever it knocks."
  def pending, do: Exception.message(%Pending{})

  @doc """
  Approves a pending account. Run by hand by nvoi's operators on the live server, and by nothing else, until
  confirmation emails exist (#74).
  """
  def approve!(email) do
    user =
      Nvoi.Postgres.get_by(__MODULE__, email: email |> String.trim() |> String.downcase()) ||
        raise ArgumentError, "no account for #{email}"

    Nvoi.Postgres.update!(change(user, pending_approval: false))
  end

  def authenticate_by(email, password) do
    user = Nvoi.Postgres.get_by(__MODULE__, email: email |> String.trim() |> String.downcase())

    if user && Bcrypt.verify_pass(password, user.password_digest) do
      user
    else
      Bcrypt.no_user_verify()
      nil
    end
  end

  @doc """
  How a commit names them as its co-author: their GitHub noreply address when their account is linked, which GitHub
  attributes to them, else their name and the address they sign in with.
  """
  def coauthor(user) do
    address =
      if user.github_user_id && user.github_login not in [nil, ""],
        do: "#{user.github_user_id}+#{user.github_login}@users.noreply.github.com",
        else: user.email

    "#{name(user)} <#{address}>"
  end
end
