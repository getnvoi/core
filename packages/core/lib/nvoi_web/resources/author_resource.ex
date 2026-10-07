defmodule NvoiWeb.AuthorResource do
  use NvoiWeb.Resource

  def one(user),
    do: %{id: user.id, name: Nvoi.User.name(user), avatar_url: NvoiWeb.UserResource.avatar(user)}
end
