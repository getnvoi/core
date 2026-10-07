defmodule Nvoi.ServiceRun do
  @moduledoc """
  A command a person runs in a production service, its output answered:

  - `live`: a pod of the service is running; the command runs in it (`kubectl exec`).
  - `oneoff`: none runs (0 replicas, or crashed); a pod of the current deployment's image, with the production
    variables and the service's resources but the given command instead of the service's, runs it once and is
    removed. The service's own command never starts.

  `variables` are for this run only: sealed until it ends, then dropped; the row keeps their names. The output keeps
  its last bytes, the workspace's secrets and every variable's value masked. Status: `queued`, `running`, `done` (`exit_status` is the
  command's), `failed` (nvoi could not run it: `error`).
  """
  use Nvoi.Schema

  schema "service_runs" do
    field :service, :string
    field :command, :string
    field :variables, Nvoi.Encrypted, redact: true
    field :variable_keys, {:array, :string}, default: []
    field :status, :string, default: "queued"
    field :mode, :string
    field :pod, :string
    field :exit_status, :integer
    field :output, :string
    field :error, :string
    field :finished_at, :utc_datetime_usec
    belongs_to :environment, Nvoi.Environment
    belongs_to :user, Nvoi.User
    timestamps()
  end

  require Logger
  alias Nvoi.{Cluster, Postgres}
  alias Nvoi.Deployment.Manifests

  # Seconds a one-off pod may run before Kubernetes stops it; a command in a live pod is bounded by SSH's own limit
  # (Compute.Cluster.Ssh, 10 minutes).
  @limit 3600
  # Seconds between two looks at a one-off pod.
  @poll 2
  # Bytes of output kept and answered, its end.
  @output_bytes 200_000

  def topic(id), do: "service_runs:#{id}"
  def finished?(run), do: run.status in ~w(done failed)

  @doc "The job's work: the command run once, in a live pod or a one-off one. A run found running was cut by a restart."
  def run!(run) do
    case Postgres.update_all(
           from(r in __MODULE__, where: r.id == ^run.id and r.status == "queued", select: r),
           set: [status: "running", updated_at: DateTime.utc_now()]
         ) do
      {1, [run]} ->
        execute(run)

      _ ->
        run = Postgres.reload!(run)

        if run.status == "running",
          do:
            finish!(run,
              status: "failed",
              error:
                "The server restarted while it ran: it may have run in part. Look before running it again."
            )
    end
  end

  defp execute(run) do
    environment = Postgres.get!(Nvoi.Environment, run.environment_id)
    cluster = Cluster.of(environment)

    if cluster == nil or cluster.status != "ready",
      do: raise("#{environment.name} has no ready cluster")

    service =
      Postgres.get_by(Nvoi.Service, environment_id: environment.id, name: run.service) ||
        raise("#{environment.name} has no service #{run.service}")

    variables = if run.variables, do: Jason.decode!(run.variables), else: %{}
    masked = Nvoi.Deployment.secret_values(environment, variables)

    {mode, pod, output, status} =
      case running_pod(cluster, service) do
        nil -> oneoff(run, environment, cluster, service, variables)
        pod -> live(run, cluster, service, pod, variables)
      end

    finish!(run,
      status: "done",
      mode: mode,
      pod: pod,
      exit_status: status,
      output: output |> Nvoi.Secrets.mask(masked) |> last_bytes()
    )
  rescue
    e ->
      Logger.warning("service run #{run.id}: #{Exception.message(e)}")
      finish!(run, status: "failed", error: e |> Exception.message() |> String.slice(0, 2000))
  end

  @doc "The name of a running pod of the service, or nil."
  def running_pod(cluster, service) do
    {output, status} =
      Cluster.kubectl(
        cluster,
        "get pods -n #{Manifests.namespace()} -l #{Manifests.service_label()}=#{service.name} " <>
          "--field-selector=status.phase=Running -o jsonpath={.items[*].metadata.name}"
      )

    if status == 0, do: output |> String.split() |> List.first()
  end

  # The command read by the pod's shell from stdin, after the run's variables: nothing of it on a command line.
  defp live(run, cluster, service, pod, variables) do
    script = exports(variables) <> run.command <> "\n"

    {output, status} =
      Cluster.kubectl(
        cluster,
        "exec -i -n #{Manifests.namespace()} #{pod} -c #{service.name} -- sh -s",
        script
      )

    {"live", pod, output, status}
  end

  defp oneoff(run, environment, cluster, service, variables) do
    deployment =
      Nvoi.Deployment.current(environment) ||
        raise "nothing is deployed on #{environment.name}: apply an image before running a command in #{service.name}"

    name = "run-" <> String.slice(String.replace(run.id, "-", ""), -12, 12)
    namespace = Manifests.namespace()

    try do
      apply!(cluster, oneoff_objects(name, run, service, deployment, variables))
      phase = await(cluster, name, System.monotonic_time(:second) + @limit + 60)

      {output, _} = Cluster.kubectl(cluster, "logs -n #{namespace} #{name}")

      {code, _} =
        Cluster.kubectl(
          cluster,
          "get pod -n #{namespace} #{name} -o jsonpath={.status.containerStatuses[0].state.terminated.exitCode}"
        )

      status =
        case Integer.parse(String.trim(code)) do
          {n, _} -> n
          :error -> if phase == "Succeeded", do: 0, else: 1
        end

      {"oneoff", name, output, status}
    after
      Cluster.kubectl(
        cluster,
        "delete pod,secret -n #{namespace} #{name} --ignore-not-found --wait=false"
      )
    end
  end

  @doc "The one-off pod and the Secret of its run's variables, as one Kubernetes List."
  def oneoff_objects(name, run, service, deployment, variables) do
    labels = %{"app.kubernetes.io/managed-by" => "nvoi", "nvoi.to/run" => name}

    container =
      service
      |> Manifests.container(deployment.commit)
      |> Map.delete("ports")
      |> Map.put("command", ["sh", "-c", run.command])
      |> Map.put(
        "envFrom",
        [%{"secretRef" => %{"name" => Manifests.secret()}}, %{"secretRef" => %{"name" => name}}]
      )

    %{
      "apiVersion" => "v1",
      "kind" => "List",
      "items" => [
        %{
          "apiVersion" => "v1",
          "kind" => "Secret",
          "type" => "Opaque",
          "metadata" => %{
            "name" => name,
            "namespace" => Manifests.namespace(),
            "labels" => labels
          },
          "data" => Map.new(variables, fn {k, v} -> {k, Base.encode64(v)} end)
        },
        %{
          "apiVersion" => "v1",
          "kind" => "Pod",
          "metadata" => %{
            "name" => name,
            "namespace" => Manifests.namespace(),
            "labels" => labels
          },
          "spec" => %{
            "restartPolicy" => "Never",
            "enableServiceLinks" => false,
            "activeDeadlineSeconds" => @limit,
            "containers" => [container]
          }
        }
      ]
    }
  end

  defp apply!(cluster, list) do
    case Cluster.kubectl(cluster, Manifests.apply_args(), Jason.encode!(list)) do
      {_, 0} -> :ok
      {output, _} -> raise "starting the one-off pod failed: #{String.slice(output, 0, 1000)}"
    end
  end

  defp await(cluster, name, until) do
    {phase, _} =
      Cluster.kubectl(
        cluster,
        "get pod -n #{Manifests.namespace()} #{name} -o jsonpath={.status.phase}"
      )

    cond do
      String.trim(phase) in ~w(Succeeded Failed) ->
        String.trim(phase)

      System.monotonic_time(:second) > until ->
        raise "the one-off pod #{name} did not finish (#{String.trim(phase)})"

      true ->
        Process.sleep(@poll * 1000)
        await(cluster, name, until)
    end
  end

  defp exports(variables) do
    Enum.map_join(variables, fn {k, v} ->
      "export #{k}='#{String.replace(v, "'", "'\\''")}'\n"
    end)
  end

  @doc "Output with the workspace's secrets and the environment's variables' values masked."
  def masked_output(environment, output),
    do: Nvoi.Secrets.mask(output, Nvoi.Deployment.secret_values(environment, %{}))

  defp last_bytes(output) when byte_size(output) <= @output_bytes, do: output

  defp last_bytes(output) do
    tail = binary_part(output, byte_size(output) - @output_bytes, @output_bytes)
    "…" <> (String.chunk(tail, :valid) |> Enum.filter(&String.valid?/1) |> Enum.join())
  end

  defp finish!(run, set) do
    run =
      Postgres.update!(
        change(
          Postgres.reload!(run),
          Keyword.merge(set, variables: nil, finished_at: DateTime.utc_now())
        )
      )

    Phoenix.PubSub.broadcast(Nvoi.PubSub, topic(run.id), {:service_run, run.id})
    run
  end
end
