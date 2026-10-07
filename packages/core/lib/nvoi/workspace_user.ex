defmodule Nvoi.WorkspaceUser do
  use Nvoi.Schema

  schema "workspace_users" do
    field :archived_at, :utc_datetime_usec
    field :role, :string, default: "editor"
    belongs_to :user, Nvoi.User
    belongs_to :workspace, Nvoi.Workspace
    has_many :grants, Nvoi.Grant
    has_many :tool_grants, Nvoi.ToolGrant
    timestamps()
  end

  @doc "The rows nobody archived: an archived one is out of every list and every reach."
  def live, do: from(r in __MODULE__, where: is_nil(r.archived_at))

  @doc "A workspace without seats keeps one member, whichever way the second one comes."
  def changeset(member, attrs) do
    member
    |> cast(attrs, [:role])
    |> validate_inclusion(:role, ~w(editor manager admin), message: "is not included in the list")
    |> unsafe_validate_unique([:user_id, :workspace_id], Nvoi.Postgres, query: live())
    |> then(fn c -> if member.id == nil, do: validate_seats(c), else: c end)
  end

  defp validate_seats(changeset) do
    workspace = Nvoi.Postgres.get!(Nvoi.Workspace, get_field(changeset, :workspace_id))

    case Nvoi.Billing.member_refusal(workspace) do
      nil -> changeset
      message -> add_error(changeset, :base, message)
    end
  end

  @doc """
  Removes the member: archived, out of the workspace's members and every reach, their sign-ins refused here. Their
  admissions and tool levels stay with the archived row; adding them back makes a new member.
  """
  def archive!(member), do: Nvoi.Postgres.update!(change(member, archived_at: DateTime.utc_now()))

  @doc "The sign-ins a removed member was using here, to close their sockets."
  def sign_ins(member),
    do:
      Nvoi.Postgres.all(
        from s in Nvoi.Session,
          where: s.user_id == ^member.user_id and s.workspace_id == ^member.workspace_id,
          select: s.id
      )
end
