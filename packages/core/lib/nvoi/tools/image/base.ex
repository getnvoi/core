defmodule Nvoi.Tools.Image.Base do
  @moduledoc "The image named by the path, through the environments in reach."
  import Ecto.Query
  alias Nvoi.{Access, Postgres}

  def image(params, current) do
    reach = from e in Access.environments(current.access), select: e.id

    Postgres.one!(
      from i in Nvoi.Image,
        where: i.id == ^to_string(params["id"]) and i.environment_id in subquery(reach)
    )
  end
end
