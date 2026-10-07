defmodule NvoiWeb.HooksTest do
  use NvoiWeb.ConnCase, async: false
  setup {Nvoi.GitHubFixtures, :on}
  import Nvoi.TaskFixtures
  import Ecto.Query
  alias Nvoi.{Postgres, Task, Webhook}
  alias Nvoi.GitHubApp
  alias Nvoi.Connectors.Delivery

  setup do
    :ets.delete_all_objects(NvoiWeb.Hooks.TasksController)
    place = place(host: "github")
    installation = 7_000_000 + System.unique_integer([:positive])

    Postgres.insert!(%Nvoi.GitHubInstallation{
      kind: "app",
      source: "github",
      github_installation_id: installation,
      account_login: "acme",
      account_type: "Organization",
      workspace_id: place.workspace.id
    })

    Map.put(place, :installation, installation)
  end

  defp runs(task), do: Postgres.all(from s in Task.runs(task), order_by: s.created_at)

  describe "a task's webhook" do
    test "a call with its token starts a run with the text as data; another token is refused",
         %{
           conn: conn
         } = place do
      task = task(place, %{"trigger" => "webhook"})
      token = Task.regenerate_token!(task)

      refused =
        conn
        |> put_req_header("authorization", "Bearer nvt_wrong")
        |> post("/hooks/tasks/#{task.id}")

      assert json_response(refused, 401) == %{
               "error" => "No task with that id and token.",
               "code" => "unauthorized"
             }

      started =
        build_conn()
        |> put_req_header("authorization", "Bearer #{token}")
        |> put_req_header("content-type", "application/json")
        |> post("/hooks/tasks/#{task.id}", Jason.encode!(%{text: "Sentry: 500 on /login"}))

      %{"session_id" => id, "session_url" => url} = json_response(started, 201)
      assert url =~ "/sessions/#{id}"
      [session] = runs(task)
      assert session.id == id
      [turn] = Postgres.all(from t in Nvoi.Turn, where: t.agent_session_id == ^id)
      assert turn.prompt =~ "<task-payload>\nSentry: 500 on /login\n</task-payload>"

      plain =
        build_conn()
        |> put_req_header("authorization", "Bearer #{token}")
        |> put_req_header("content-type", "text/plain")
        |> post("/hooks/tasks/#{task.id}", "raw words")

      assert json_response(plain, 201)
      assert length(runs(task)) == 2
    end

    test "twenty calls an hour, whatever their token", %{conn: conn} = place do
      task = task(place, %{"trigger" => "webhook"})
      for _ <- 1..20, do: post(build_conn(), "/hooks/tasks/#{task.id}")
      limited = post(conn, "/hooks/tasks/#{task.id}")
      assert json_response(limited, 429)["code"] == "too_many_requests"
    end
  end

  describe "a connector's deliveries" do
    defp deliver(payload, opts \\ []) do
      body = Jason.encode!(payload)
      delivery = opts[:delivery] || Ecto.UUID.generate()

      signature =
        "sha256=" <>
          Base.encode16(:crypto.mac(:hmac, :sha256, GitHubApp.webhook_secret(), body),
            case: :lower
          )

      conn =
        build_conn()
        |> put_req_header("content-type", "application/json")
        |> put_req_header("x-github-event", opts[:event] || "issue_comment")
        |> put_req_header("x-github-delivery", delivery)
        |> put_req_header("x-hub-signature-256", signature)
        |> post(opts[:path] || "/hooks/github", body)

      assert conn.status == 200
      Postgres.get_by(Webhook, delivery_id: delivery)
    end

    defp comment(place, body \\ "Please look", association \\ "MEMBER") do
      %{
        action: "created",
        installation: %{id: place.installation},
        repository: %{full_name: place.repo.name},
        sender: %{login: "ben"},
        issue: %{number: 3, title: "Login fails", labels: []},
        comment: %{body: body, author_association: association}
      }
    end

    defp listening(place),
      do:
        task(place, %{
          "trigger" => "github",
          "event" => "issue_comment",
          "actions" => "created"
        })

    test "an unknown connector is not found, and a bad signature is refused", %{conn: conn} do
      assert conn
             |> put_req_header("content-type", "application/json")
             |> post("/hooks/nope", "{}")
             |> response(404)

      assert build_conn()
             |> put_req_header("x-hub-signature-256", "x")
             |> put_req_header("content-type", "application/json")
             |> post("/hooks/github", "{}")
             |> response(401)
    end

    test "a delivery keeps what it did to each task, and the session keeps its subject", place do
      task = listening(place)
      webhook = deliver(comment(place))
      [session] = runs(task)
      assert webhook.outcomes == %{task.id => "started"}
      assert session.subject == "github:3" and session.digest != nil
      [turn] = Postgres.all(from t in Nvoi.Turn, where: t.agent_session_id == ^session.id)
      assert turn.prompt =~ ~s("comment": "Please look")
      assert turn.prompt =~ ~s("event": "issue_comment")
      assert Map.has_key?(webhook.headers, "x-hub-signature-256")
      assert webhook.processed_at

      webhook = deliver(%{comment(place) | action: "edited"})
      assert webhook.outcomes == %{task.id => "filtered"}
    end

    test "somebody from outside the repository starts nothing, a label from anyone does", place do
      task = listening(place)
      webhook = deliver(comment(place, "hi", "NONE"))
      assert webhook.outcomes == %{task.id => "untrusted"}
      assert runs(task) == []

      task =
        Postgres.update!(Task.changeset(task, %{"event" => "issues", "actions" => "labeled"}))

      labeled = %{
        action: "labeled",
        installation: %{id: place.installation},
        repository: %{full_name: place.repo.name},
        sender: %{login: "ben"},
        issue: %{number: 4, author_association: "NONE", labels: [%{name: "nvoi"}]}
      }

      assert deliver(labeled, event: "issues").outcomes == %{task.id => "started"}
    end

    test "a filter picks its deliveries", place do
      task =
        task(place, %{
          "trigger" => "github",
          "event" => "issues",
          "actions" => "labeled",
          "filters" => %{"label" => "nvoi"}
        })

      issue = fn label ->
        %{
          action: "labeled",
          installation: %{id: place.installation},
          repository: %{full_name: place.repo.name},
          sender: %{login: "ben"},
          issue: %{number: 5, title: label, labels: [%{name: label}]}
        }
      end

      assert deliver(issue.("bug"), event: "issues").outcomes == %{task.id => "filtered"}
      assert deliver(issue.("nvoi"), event: "issues").outcomes == %{task.id => "started"}
    end

    test "what the App did itself starts nothing", place do
      task = listening(place)

      webhook =
        deliver(%{comment(place) | sender: %{login: "#{GitHubApp.slug()}[bot]"}})

      assert webhook.outcomes == %{task.id => "own"}
    end

    test "the same summary within the window starts one run, and a task starts ten an hour",
         place do
      task = listening(place)
      deliver(comment(place))
      assert deliver(comment(place)).outcomes[task.id] == "deduped"

      for n <- 1..(Delivery.cap() - 1), do: deliver(comment(place, "Look #{n}"))
      assert length(runs(task)) == Delivery.cap()
      assert deliver(comment(place, "One more")).outcomes[task.id] == "capped"
    end

    test "another repository's delivery does not reach the task", place do
      listening(place)
      webhook = deliver(%{comment(place) | repository: %{full_name: "someone/else"}})
      assert webhook.outcomes == %{}
    end

    test "a delivery held is applied again but fires nothing twice; the App's path still delivers",
         place do
      task = listening(place)
      delivery = Ecto.UUID.generate()
      deliver(comment(place), delivery: delivery, path: "/webhooks/github")
      deliver(comment(place, "Again"), delivery: delivery)
      assert length(runs(task)) == 1
    end

    test "a delivery a failure left unapplied is applied again by the tasks job", place do
      task = listening(place)

      Postgres.insert!(%Webhook{
        source: "github",
        delivery_id: Ecto.UUID.generate(),
        event: "issue_comment",
        payload: Jason.decode!(Jason.encode!(comment(place))),
        received_at: DateTime.add(DateTime.utc_now(), -300)
      })

      Nvoi.Jobs.TasksJob.perform(%Oban.Job{})
      assert length(runs(task)) == 1
    end

    test "deliveries keep the installation and the worktrees' pull requests current", place do
      worktree = Nvoi.Worktree.create!(place.repo, place.environment)

      closed = %{
        action: "closed",
        installation: %{id: place.installation, account: %{login: "acme", type: "Organization"}},
        repository: %{full_name: place.repo.name},
        sender: %{login: "ben"},
        pull_request: %{number: 9, merged: true, state: "closed", head: %{ref: worktree.branch}}
      }

      deliver(closed, event: "pull_request")
      assert Postgres.get!(Nvoi.Worktree, worktree.id).pull_request_state == "merged"

      deliver(%{action: "suspend", installation: %{id: place.installation}},
        event: "installation"
      )

      [row] =
        Postgres.all(
          from c in Nvoi.GitHubInstallation,
            where: c.github_installation_id == ^place.installation
        )

      assert row.suspended_at != nil

      renamed = %{
        action: "edited",
        changes: %{default_branch: %{from: "main"}},
        repository: %{full_name: place.repo.name, default_branch: "trunk"}
      }

      deliver(renamed, event: "repository")
      assert Postgres.get!(Nvoi.Repo, place.repo.id).default_branch == "trunk"
      assert Postgres.get!(Nvoi.Environment, place.environment.id).branch == "trunk"
    end
  end
end
