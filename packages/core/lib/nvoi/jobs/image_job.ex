defmodule Nvoi.Jobs.ImageJob do
  @moduledoc """
  Builds an image in its repository's build machine (Nvoi.Image.build!/1); while the machine builds another image it
  tries again a little later.
  """
  use Oban.Worker, queue: :production, max_attempts: 1

  def perform(%{args: %{"id" => id}}) do
    with %Nvoi.Image{} = image <- Nvoi.Postgres.get(Nvoi.Image, id),
         :busy <- Nvoi.Image.build!(image),
         do: {:snooze, Nvoi.Image.busy_seconds()},
         else: (_ -> :ok)
  end
end
