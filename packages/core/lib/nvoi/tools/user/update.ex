defmodule Nvoi.Tools.User.Update do
  @moduledoc """
  The signed-in person's own name and picture. `avatar`, sent as multipart, replaces the picture; a true
  `remove_avatar` takes it away. Fields not sent stay.
  """
  use Nvoi.Tools.Action,
    group: "user",
    action: "update",
    category: :account,
    summary: "Name yourself and pick a picture",
    patch: "/me",
    access: :sensitive,
    default_role: :open,
    content: [:form]

  param :last_name, :string, required: true

  def call(params, current) do
    user = current.user

    attrs =
      params
      |> Map.take(~w(first_name last_name))
      |> Map.new(fn {k, v} -> {k, Nvoi.Text.presence(String.trim(to_string(v)))} end)

    user = Postgres.update!(Ecto.Changeset.cast(user, attrs, [:first_name, :last_name]))

    if params["remove_avatar"] in [true, "true", "1"] or match?(%Plug.Upload{}, params["avatar"]) do
      Postgres.delete_all(
        from a in Nvoi.Attachment,
          where: a.record_type == "User" and a.record_id == ^user.id and a.name == "avatar"
      )
    end

    with %Plug.Upload{} = upload <- params["avatar"] do
      Nvoi.Storage.attach!(
        "User",
        user.id,
        "avatar",
        upload.filename,
        File.read!(upload.path),
        upload.content_type
      )
    end

    %{user: NvoiWeb.UserResource.wrap(user)}
  end
end
