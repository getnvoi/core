defmodule Nvoi.Machines do
  @moduledoc """
  What every machine shares: its provider, its boxd token and its box. A token is derived from this installation's
  secret and the machine's name; boxd is installed with it through the provider, and a fork, which wakes holding its
  seed's, is given its own through boxd.
  """
  alias Compute.{Box, Layout}

  def provider(name), do: Compute.build(name)

  def token(name) do
    :crypto.mac(:hmac, :sha256, Nvoi.Credentials.fetch!(["secret_key_base"]), "box:" <> name)
    |> Base.encode16(case: :lower)
  end

  @doc """
  The labels a machine is made with: what holds it, and this installation, so a provider org shared by several
  installations (development databases side by side) tells which machines are whose.
  """
  def labels(holder), do: Map.put(holder, "nvoi-installation", installation())

  @doc "This installation: its secret and its database, so copies of one database are told apart."
  def installation do
    database = Nvoi.Postgres.config()[:database]

    :crypto.mac(
      :hmac,
      :sha256,
      Nvoi.Credentials.fetch!(["secret_key_base"]),
      "installation:" <> database
    )
    |> Base.encode16(case: :lower)
    |> binary_part(0, 12)
  end

  def machine(box_id, name),
    do: %Compute.Machine{id: box_id, name: name, state: :running, data: %{}}

  @doc "The box of a running machine, as `token_of` names it, waiting until boxd answers."
  def box({module, key}, machine, name, wait \\ 120) do
    reach =
      module.preview(key, machine, Box.port()) ||
        raise(Compute.Error, message: "#{name} gives no address for boxd")

    wait_for(Box.new(reach, token(name)), System.monotonic_time(:second) + wait, name)
  end

  def bootstrap!({module, key}, machine, name),
    do: module.bootstrap(key, machine, Nvoi.Boxd.binary(), token(name))

  @doc "A fork's boxd still holds its seed's token: it writes its own and restarts, its commands kept."
  def retoken!(provider, machine, seed_name, name) do
    {module, key} = provider
    reach = module.preview(key, machine, Box.port())

    seed_box =
      wait_for(Box.new(reach, token(seed_name)), System.monotonic_time(:second) + 120, name)

    Box.start(seed_box, [
      "bash",
      "-c",
      "sleep 1; umask 077; printf 'NVOI_TOKEN=%s\\n' '#{token(name)}' > /etc/nvoi/env; systemctl restart nvoi-boxd"
    ])

    Process.sleep(3000)
    box(provider, machine, name)
  end

  defp wait_for(box, deadline, name) do
    cond do
      Box.health?(box) and authorized?(box) ->
        box

      System.monotonic_time(:second) > deadline ->
        raise Compute.Error, message: "boxd on #{name} did not answer"

      true ->
        Process.sleep(2000) && wait_for(box, deadline, name)
    end
  end

  defp authorized?(box) do
    Box.commands(box)
    true
  rescue
    Compute.Error -> false
  end

  def user, do: Layout.user()
end
