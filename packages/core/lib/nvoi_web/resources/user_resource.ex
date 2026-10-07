defmodule NvoiWeb.UserResource do
  use NvoiWeb.Resource

  def one(user) do
    %{
      id: user.id,
      email: user.email,
      first_name: user.first_name,
      last_name: user.last_name,
      name: Nvoi.User.name(user),
      avatar_url: avatar(user),
      # Until nvoi approves it, the account reads who it is and signs out, nothing else.
      pending_approval: Nvoi.User.pending?(user),
      created_at: user.created_at
    }
  end

  @doc "Where the person's picture is served inline, a path on the API's host; nil until they chose one."
  def avatar(user) do
    blob = Nvoi.Attachment.blob("User", user.id, "avatar")
    blob && Nvoi.Blob.path(blob)
  end
end
