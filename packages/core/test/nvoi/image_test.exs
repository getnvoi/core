defmodule Nvoi.ImageTest do
  # A production environment's commit built into an image in the environment's build machine (faked): the source
  # sent from here, the build's lines logged, the tarball uploaded by the machine with a URL signed for it, reused,
  # kept within the environment's limit; asked for by a person or by an agent's session.
  use Nvoi.DataCase, async: false
  import Nvoi.ProductionFixtures
  alias Nvoi.{Builder, Deployment, FakeSandbox, Image, Tools}
  alias Nvoi.Tools.Action

  setup do
    forge!()
    bucket!()
    FakeSandbox.use()
    ctx = place(repo: &app/1)
    Map.put(ctx, :main, Image.resolve(ctx.repo, "main"))
  end

  defp run_images do
    for job <-
          Postgres.all(
            from j in Oban.Job,
              where: j.worker == "Nvoi.Jobs.ImageJob" and j.state == "available",
              order_by: j.id
          ) do
      Postgres.update!(Ecto.Changeset.change(job, state: "completed"))
      Nvoi.Jobs.ImageJob.perform(job)
    end
  end

  defp build(ctx, params \\ %{}),
    do: Action.run(Tools.Image.Build, Map.put(params, "id", ctx.production.id), ctx.person)

  defp log(image), do: Enum.map_join(Image.lines(image), "\n", &"#{&1.step}: #{&1.output}")

  defp received do
    Stream.repeatedly(fn ->
      receive do
        {:sandbox, _, _} = m -> m
        {:sandbox, _, _, _} = m -> m
      after
        0 -> nil
      end
    end)
    |> Enum.take_while(& &1)
  end

  test "a build: the source sent to the environment's machine, docker's lines, an upload signed for one object",
       ctx do
    assert {:accepted, %{image: answer}} = build(ctx)
    assert answer.status == "queued" and answer.commit == ctx.main
    run_images()

    image = Postgres.get!(Image, answer.id)
    assert image.status == "ready"
    assert image.digest == "sha256:" <> String.duplicate("ab", 32) and image.size == 4096

    assert image.storage_key ==
             "images/#{ctx.workspace.id}/#{ctx.repo.id}/#{ctx.production.id}/#{ctx.main}.tar.gz"

    builder = Postgres.get!(Builder, image.builder_id)

    assert builder.environment_id == ctx.production.id and
             builder.name =~ ~r/\Abuild-[0-9a-f]{16}\z/

    messages = received()

    # The source is the commit's tree, sent from here; the machine holds no credential.
    [{:sandbox, :put, source_path, source}] =
      for {:sandbox, :put, path, _} = m <- messages, String.ends_with?(path, ".src.tar.gz"), do: m

    assert source_path =~ ctx.main
    {:ok, files} = :erl_tar.extract({:binary, source}, [:compressed, :memory])
    assert Enum.any?(files, fn {name, _} -> to_string(name) =~ "Dockerfile" end)

    # The upload's URL is a file the script reads, never on its command line.
    [url] = for {:sandbox, :put, path, url} <- messages, String.ends_with?(path, ".url"), do: url
    assert url =~ ~r|\Ahttps://bucket\.test/nvoi-test/#{image.storage_key}\?.*X-Amz-Expires=1800|
    [store] = for {:sandbox, :run, script} <- messages, script =~ "docker save", do: script
    assert store =~ "curl -fsS --retry 3 -T" and not (store =~ "X-Amz")
    assert {:sandbox, :park, builder.name} in messages

    text = log(image)
    assert text =~ "build: #1 [internal] load build definition from Dockerfile"
    assert text =~ "store: ready: sha256:"

    shown = Action.run(Tools.Image.Show, %{"id" => image.id}, ctx.person)
    assert shown.image.machine == builder.name and length(shown.image.log) > 3
  end

  test "the same commit is built once; asking while it builds joins it; another environment builds its own",
       ctx do
    {:accepted, %{image: first}} = build(ctx)
    {:accepted, %{image: again}} = build(ctx)
    assert first.id == again.id
    assert [_] = Postgres.all(from j in Oban.Job, where: j.worker == "Nvoi.Jobs.ImageJob")

    other =
      Postgres.insert!(%Nvoi.Environment{
        repo_id: ctx.repo.id,
        name: "eu",
        kind: "production",
        branch: "main",
        provider_name: "daytona",
        shape: "2x4",
        disk: 10
      })

    theirs = Image.ensure!(other, ctx.main, %{user_id: ctx.user.id})
    refute theirs.id == first.id
    refute Builder.of!(other).name == Builder.of!(ctx.production).name
  end

  test "one build at a time per environment, in the order asked", ctx do
    first = Image.ensure!(ctx.production, ctx.main, %{user_id: ctx.user.id})
    second_commit = commit!(ctx.repo, %{"Dockerfile" => "FROM busybox\n"})
    second = Image.ensure!(ctx.production, second_commit, %{user_id: ctx.user.id})
    assert Image.build!(second) == :busy
    assert %{status: "ready"} = Image.build!(first)
    assert %{status: "ready"} = Image.build!(second)
  end

  test "a failed build keeps its lines and why; asking again builds it again", ctx do
    FakeSandbox.answer(fn script ->
      if script =~ "docker build",
        do: {["#4 [2/3] RUN bun install", "error: lockfile had changes"], 1},
        else: FakeSandbox.default(script)
    end)

    {:accepted, %{image: answer}} = build(ctx)
    run_images()
    image = Postgres.get!(Image, answer.id)
    assert image.status == "failed" and image.error =~ "error: lockfile had changes"
    assert log(image) =~ "build: error: lockfile had changes"

    FakeSandbox.answer(&FakeSandbox.default/1)
    assert %{status: "queued"} = Image.ensure!(ctx.production, ctx.main, %{user_id: ctx.user.id})
    run_images()
    assert Postgres.reload!(image).status == "ready"
  end

  test "only a production environment builds: the tool and the database refuse another", ctx do
    assert %Action.Refused{code: "not_production"} =
             catch_error(Action.run(Tools.Image.Build, %{"id" => ctx.development.id}, ctx.person))

    assert_raise Postgrex.Error, ~r/images are built for production environments only/, fn ->
      Postgres.insert!(%Image{environment_id: ctx.development.id, commit: ctx.main})
    end
  end

  test "past the environment's limit, images leave the bucket; the live one and the one before stay",
       ctx do
    Postgres.update!(change(ctx.production, images_kept: 1))

    images =
      for n <- 1..4 do
        commit = commit!(ctx.repo, %{"Dockerfile" => "FROM busybox\n", "n" => "#{n}"})

        Postgres.insert!(%Image{
          environment_id: ctx.production.id,
          commit: commit,
          status: "ready",
          storage_key: "images/k/#{n}",
          finished_at: DateTime.add(DateTime.utc_now(), n)
        })
      end

    [one, two, three, four] = images

    for {image, status, n} <- [{one, "rolled_back", 1}, {two, "live", 2}] do
      Postgres.insert!(%Deployment{
        environment_id: ctx.production.id,
        image_id: image.id,
        commit: image.commit,
        status: status,
        finished_at: DateTime.add(DateTime.utc_now(), n)
      })
    end

    Image.prune(ctx.production)
    assert Postgres.reload!(four).status == "ready"
    assert Postgres.reload!(three).status == "pruned"
    assert Postgres.reload!(two).status == "ready"
    assert Postgres.reload!(one).status == "ready"
    assert_received {:bucket, "DELETE", "/nvoi-test/images/k/3"}

    Postgres.update_all(from(d in Deployment, where: d.image_id == ^one.id),
      set: [status: "live"]
    )

    Postgres.update!(change(Postgres.reload!(one), status: "pruned"))
    [earlier] = Postgres.all(from d in Deployment, where: d.image_id == ^one.id)

    assert %Action.Refused{code: "image_pruned"} =
             catch_error(Action.run(Tools.Deployment.Rollback, %{"id" => earlier.id}, ctx.person))
  end

  test "an environment with images stays production", ctx do
    Image.ensure!(ctx.production, ctx.main, %{user_id: ctx.user.id})

    error =
      assert_raise Ecto.InvalidChangesetError, fn ->
        Nvoi.Environment.kind!(ctx.production, "development")
      end

    assert {"cannot go back to development: it has deployments or images", _} =
             error.changeset.errors[:kind]
  end

  describe "an agent's build_image" do
    setup ctx do
      worktree =
        Postgres.insert!(%Nvoi.Worktree{
          repo_id: ctx.repo.id,
          environment_id: ctx.development.id,
          base_ref: "main",
          branch: "main",
          slug: "w-#{System.unique_integer([:positive])}",
          provider_name: "daytona",
          provider_type: "vm",
          state: "running"
        })

      session =
        Postgres.insert!(%Nvoi.AgentSession{worktree_id: worktree.id, agent_id: ctx.agent.id})

      %{call: %{worktree: worktree, session: session, turn: nil}}
    end

    test "builds the pushed branch for the production environment and reads the image", ctx do
      text =
        Oban.Testing.with_testing_mode(:inline, fn -> Tools.BuildImage.call(%{}, ctx.call) end)

      assert text =~ "for production is ready: sha256:"
      [image] = Postgres.all(Image)
      assert image.agent_session_id == ctx.call.session.id and image.commit == ctx.main
    end

    test "reads the build's last lines when it fails", ctx do
      FakeSandbox.answer(fn script ->
        if script =~ "docker build",
          do: {["#4 RUN bun install", "error: lockfile had changes"], 1},
          else: FakeSandbox.default(script)
      end)

      error =
        assert_raise Nvoi.Tools.Error, fn ->
          Oban.Testing.with_testing_mode(:inline, fn -> Tools.BuildImage.call(%{}, ctx.call) end)
        end

      assert error.message =~ "failed" and error.message =~ "error: lockfile had changes"
    end

    test "a branch not pushed, and a repository without production, are refused with what to do",
         ctx do
      unpushed = %{ctx.call | worktree: %{ctx.call.worktree | branch: "never-pushed"}}

      assert_raise Nvoi.Tools.Error, ~r/not pushed: call push_branch first/, fn ->
        Tools.BuildImage.call(%{}, unpushed)
      end

      Postgres.update!(change(ctx.production, kind: "development"))

      assert_raise Nvoi.Tools.Error, ~r/has no production environment/, fn ->
        Tools.BuildImage.call(%{}, ctx.call)
      end
    end
  end
end
