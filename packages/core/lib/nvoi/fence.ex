defmodule Nvoi.Fence do
  @moduledoc """
  The one place that asks a box whether the cloud's metadata address is closed to its browser user before untrusted
  work runs there as that user: a converter opening an uploaded file (`Nvoi.BoxRender`). A box that answers without
  the rule `Nvoi.Steps` installs runs nothing.
  """
  # What the rule rejects, per family, as `nft list table inet nvoi` prints it back.
  @closed ["ip daddr 169.254.0.0/16 reject", "ip6 daddr fe80::/10 reject"]

  defmodule Open, do: defexception(message: "the metadata address is not closed on this box")

  def check!(box) do
    grep = Enum.map_join(@closed, " && ", &~s(printf '%s' "$rules" | grep -q '#{&1}'))

    case Compute.Box.run(box, "rules=$(nft list table inet nvoi 2>/dev/null) && #{grep}") do
      {0, _} -> :ok
      _ -> raise Open
    end
  end
end
