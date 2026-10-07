defmodule Nvoi.CloudCredentialTest do
  # The workspace's key to its cloud: checked on the provider before it is kept, sealed, never answered.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{CloudCredential, FakeCloud, Tools}
  alias Nvoi.Tools.Action

  setup do
    fake = FakeCloud.use()
    Map.put(place(), :fake, fake)
  end

  defp create(ctx, token),
    do:
      Action.run(
        Tools.CloudCredential.Create,
        %{"provider" => "hetzner", "token" => token},
        ctx.person
      )

  test "a token the provider accepts is kept sealed and answered masked", ctx do
    assert {:created, %{cloud_credential: answer}} = create(ctx, FakeCloud.token())
    assert answer.provider == "hetzner"
    assert answer.token == "…oken"
    assert answer.checked_at
    refute inspect(answer) =~ FakeCloud.token()

    [stored] =
      Postgres.all(
        from c in "cloud_credentials",
          where: c.workspace_id == type(^ctx.workspace.id, :binary_id),
          select: c.token
      )

    refute stored =~ FakeCloud.token()
    assert CloudCredential.find(ctx.workspace.id, "hetzner").token == FakeCloud.token()

    assert %{cloud_credentials: [listed]} =
             Action.run(Tools.CloudCredential.List, %{}, ctx.person)

    assert listed.token == "…oken"
  end

  test "a token the provider refuses is not kept", ctx do
    refused = catch_error(create(ctx, "wrong-token"))
    assert %Action.Refused{code: "check_failed", status: 422} = refused
    assert refused.message =~ "hetzner refused the token"
    refute refused.message =~ "wrong-token"
    assert CloudCredential.find(ctx.workspace.id, "hetzner") == nil
  end

  test "a second token replaces the first; check says when the kept one stops working", ctx do
    {:created, %{cloud_credential: first}} = create(ctx, FakeCloud.token())
    {:created, %{cloud_credential: second}} = create(ctx, FakeCloud.token())
    assert first.id == second.id

    assert %{cloud_credential: %{id: id}} =
             Action.run(Tools.CloudCredential.Check, %{"id" => first.id}, ctx.person)

    assert id == first.id

    Compute.FakeClusterProvider.revoke(ctx.fake)

    assert %Action.Refused{code: "check_failed"} =
             catch_error(Action.run(Tools.CloudCredential.Check, %{"id" => first.id}, ctx.person))

    assert Action.run(Tools.CloudCredential.Delete, %{"id" => first.id}, ctx.person) == nil
    assert CloudCredential.find(ctx.workspace.id, "hetzner") == nil
  end

  test "an unknown cloud is refused", ctx do
    assert %Action.Refused{code: "check_failed", message: message} =
             catch_error(
               Action.run(
                 Tools.CloudCredential.Create,
                 %{"provider" => "aws", "token" => "x"},
                 ctx.person
               )
             )

    assert message =~ "aws is not a cloud nvoi makes clusters on"
  end

  test "no agent reaches the production tools through the gateway" do
    production = for a <- Nvoi.Tools.actions(), a.__tool__().production, do: a.tool_name()
    assert "cloud_credential_create" in production

    for name <- production do
      entry = Nvoi.Tools.Gateway.find_entry(name)
      assert entry.callable == false, name
      assert entry.reason == "Production is a person's: agents never reach it."
    end
  end
end
