defmodule Nvoi.GitHubInstallation do
  use Nvoi.Schema

  schema "connectors" do
    field :archived_at, :utc_datetime_usec
    field :account_login, :string
    field :account_type, :string
    field :github_installation_id, :integer
    field :installed_at, :utc_datetime_usec
    belongs_to :installer, Nvoi.User
    field :kind, :string, default: "token"
    field :repos_sync_status, :string, default: "idle"
    field :repos_synced_at, :utc_datetime_usec
    field :source, :string
    field :suspended_at, :utc_datetime_usec
    field :token, Nvoi.Encrypted
    belongs_to :workspace, Nvoi.Workspace
    has_many :github_repositories, Nvoi.GitHubRepository, foreign_key: :github_installation_id
    has_many :repos, Nvoi.Repo, foreign_key: :github_installation_id
    timestamps()
  end

  @doc "The rows nobody archived: an archived one is out of every list and every reach."
  def live, do: from(r in __MODULE__, where: is_nil(r.archived_at))

  alias Nvoi.Postgres

  alias Nvoi.GitHub
  @read %{metadata: "read"}
  # An install takes a few clicks; an older intent must not bind today's delivery.
  @intent 3600

  @doc "The workspace's GitHub connections: rows of `connectors` whose source is github."
  def of_workspace(workspace_id),
    do: from(c in live(), where: c.workspace_id == ^workspace_id and c.source == "github")

  def apps, do: from(c in live(), where: c.source == "github" and c.kind == "app")

  @doc """
  What GitHub says about an installation, on every workspace's row for it; the first row is made, in no workspace
  yet, when there is none. Returns the row no workspace holds, else the oldest. Two deliveries racing on the insert:
  the loser reads the winner's row.
  """
  def upsert(github_installation_id, attrs) do
    rows = where(apps(), [c], c.github_installation_id == ^github_installation_id)

    Postgres.update_all(rows,
      set: Keyword.merge(Map.to_list(attrs), updated_at: DateTime.utc_now())
    )

    Postgres.one(where(rows, [c], is_nil(c.workspace_id))) ||
      Postgres.one(from c in rows, order_by: c.id, limit: 1) ||
      Postgres.insert!(
        struct(
          %__MODULE__{
            github_installation_id: github_installation_id,
            kind: "app",
            source: "github",
            installed_at: DateTime.utc_now()
          },
          attrs
        )
      )
  rescue
    Ecto.ConstraintError ->
      Postgres.one!(
        from c in apps(),
          where: c.github_installation_id == ^github_installation_id,
          order_by: c.id,
          limit: 1
      )
  end

  @doc """
  Connects an installation to a workspace: its own row when it has one, else the row no workspace holds yet, else a
  row of its own beside the other workspaces'. The claim of a free row is predicated on it being free, so two
  workspaces cannot both take it. :missing when nvoi has never heard of the installation.
  """
  def claim(github_installation_id, workspace_id, installer_id) do
    rows = where(apps(), [c], c.github_installation_id == ^github_installation_id)

    case Postgres.one(from c in rows, order_by: c.id, limit: 1) do
      nil ->
        :missing

      known ->
        row =
          Postgres.one(where(rows, [c], c.workspace_id == ^workspace_id)) ||
            Enum.find_value(Postgres.all(where(rows, [c], is_nil(c.workspace_id))), fn free ->
              {claimed, _} =
                Postgres.update_all(
                  from(c in rows, where: c.id == ^free.id and is_nil(c.workspace_id)),
                  set: [
                    workspace_id: workspace_id,
                    installer_id: installer_id,
                    updated_at: DateTime.utc_now()
                  ]
                )

              claimed == 1 && Postgres.get!(__MODULE__, free.id)
            end) ||
            Postgres.insert!(%__MODULE__{
              github_installation_id: github_installation_id,
              kind: "app",
              source: "github",
              workspace_id: workspace_id,
              installer_id: installer_id,
              account_login: known.account_login,
              account_type: known.account_type,
              installed_at: known.installed_at,
              suspended_at: known.suspended_at
            })

        row =
          if row.installer_id,
            do: row,
            else: Postgres.update!(change(row, installer_id: installer_id))

        sync_later(row, true)
    end
  rescue
    Ecto.ConstraintError ->
      Postgres.one!(
        from c in apps(),
          where:
            c.github_installation_id == ^github_installation_id and
              c.workspace_id == ^workspace_id
      )
  end

  defp intent(user_id) do
    since = DateTime.add(DateTime.utc_now(), -@intent)

    Postgres.one(
      from i in Nvoi.GitHubInstallIntent,
        where: i.user_id == ^user_id and i.created_at >= ^since,
        order_by: [desc: i.created_at],
        limit: 1
    )
  end

  @doc """
  The workspace a webhook's sender stands for: where they last asked to install from, else where they last connected
  one. Nil when the sender never connected GitHub here.
  """
  def workspace_for(user_id) do
    case intent(user_id) do
      %{workspace_id: id} ->
        id

      nil ->
        Postgres.one(
          from c in live(),
            where: c.installer_id == ^user_id and not is_nil(c.workspace_id),
            order_by: [desc: c.updated_at],
            limit: 1,
            select: c.workspace_id
        )
    end
  end

  @doc """
  The delivery's sender connects the installation to the workspace they stand for. A workspace they only connected
  once before takes a free row, never a second one; the one they are installing for takes it either way.
  """
  def link(github_installation_id, %{"id" => github_user_id}) when is_integer(github_user_id) do
    with %Nvoi.User{} = user <- Postgres.get_by(Nvoi.User, github_user_id: github_user_id),
         workspace_id when not is_nil(workspace_id) <- workspace_for(user.id) do
      intent = intent(user.id)
      rows = where(apps(), [c], c.github_installation_id == ^github_installation_id)
      held = Postgres.exists?(where(rows, [c], c.workspace_id == ^workspace_id))
      free = Postgres.exists?(where(rows, [c], is_nil(c.workspace_id)))
      intended = intent != nil and intent.workspace_id == workspace_id

      if not (held and intent == nil) and (free or intended) do
        row = claim(github_installation_id, workspace_id, user.id)

        if intent && is_struct(row, __MODULE__),
          do:
            Nvoi.GitHubInstallIntent.settle!(
              intent,
              "connected",
              "Connected #{row.account_login}"
            )
      end
    end

    :ok
  end

  def link(_, _), do: :ok

  @doc """
  Removes the connection: archived with its cached repositories, out of the lists and of every token it gave. The
  repositories imported through it stay, reached by another connection to their account if there is one.
  """
  def archive!(installation) do
    installation = Postgres.update!(change(installation, archived_at: DateTime.utc_now()))
    signal(installation.workspace_id)
    installation
  end

  @doc """
  Tells a workspace's apps its connections changed: the installations, the repositories they reach, and onboarding,
  which lists both.
  """
  def signal(workspace_id),
    do: for(type <- ~w(github repos onboarding), do: Nvoi.Live.signal(workspace_id, type))

  def suspended?(installation), do: installation.suspended_at != nil
  def syncing?(installation), do: installation.repos_sync_status in ~w(pending syncing)

  @doc "A token narrowed to `permissions` (and `repositories`): the App's per call, or a person's own."
  def access_token(%{kind: "token"} = installation, _permissions, _repositories),
    do: installation.token

  def access_token(installation, permissions, repositories) do
    if suspended?(installation),
      do:
        raise(GitHub.Error,
          message: "#{installation.account_login} suspended the nvoi app on GitHub"
        )

    GitHub.installation_token(installation.github_installation_id, repositories, permissions)
  end

  def github(installation, repositories \\ nil),
    do: GitHub.new(access_token(installation, @read, repositories))

  def reported_repositories(%{kind: "token"} = i), do: GitHub.repositories(github(i))
  def reported_repositories(i), do: GitHub.installation_repositories(github(i))

  def sync_later(installation, force \\ false) do
    if force or not syncing?(installation) do
      installation = Postgres.update!(change(installation, repos_sync_status: "pending"))
      Oban.insert!(Nvoi.Jobs.GitHubSyncJob.new(%{id: installation.id}))
      installation
    else
      installation
    end
  end

  def replace_repositories(installation, reported) do
    Postgres.transaction(fn ->
      seen =
        for attrs <- reported do
          row =
            Postgres.get_by(Nvoi.GitHubRepository,
              github_installation_id: installation.id,
              github_id: attrs.github_id
            ) ||
              %Nvoi.GitHubRepository{
                github_installation_id: installation.id,
                github_id: attrs.github_id
              }

          row
          |> cast(attrs, [:full_name, :private, :pushed_at])
          |> Postgres.insert_or_update!()

          attrs.github_id
        end

      Postgres.delete_all(
        from r in Nvoi.GitHubRepository,
          where: r.github_installation_id == ^installation.id and r.github_id not in ^seen
      )
    end)
  end
end
