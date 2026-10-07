defmodule Nvoi.Deployment.Manifests do
  @moduledoc """
  What a deployment applies to its cluster, as one Kubernetes `List` in JSON: the namespace, one Secret holding the
  production environment's variables, and per service a Deployment (and a Service when it has a port). Every pod
  takes its variables from the Secret, runs the project's image of the commit (`Nvoi.Image`, `imagePullPolicy: Never`:
  imported into the node's containerd, no registry) or the service's own image, and names the commit and the
  variables' version in its annotations, so either one changing rolls the pods.

  The list goes to `kubectl apply --server-side` on stdin: the variables are never on a command line, and the server
  keeps no copy of the applied object in an annotation.
  """

  @namespace "app"
  @secret "env"
  @managed {"app.kubernetes.io/managed-by", "nvoi"}
  @service_label "nvoi.to/service"

  def namespace, do: @namespace
  def secret, do: @secret
  def service_label, do: @service_label
  def managed_selector, do: "#{elem(@managed, 0)}=#{elem(@managed, 1)}"

  @doc "`kubectl` arguments that apply the list given on stdin."
  def apply_args, do: "apply --server-side --force-conflicts --field-manager nvoi -f -"

  @doc """
  The list for `services` at `commit`, with `variables` (a map, the database's among them) in the Secret; `version`
  is the variables' version and `database` the database's (nil without one), written on each pod's template so a
  change of either rolls the pods.
  """
  def render(services, commit, variables, version, database \\ nil) do
    %{
      "apiVersion" => "v1",
      "kind" => "List",
      "items" =>
        [namespace_object(), secret_object(variables)] ++
          Enum.flat_map(services, &service_objects(&1, commit, version, database))
    }
  end

  defp namespace_object,
    do: %{
      "apiVersion" => "v1",
      "kind" => "Namespace",
      "metadata" => %{"name" => @namespace, "labels" => labels()}
    }

  defp secret_object(variables),
    do: %{
      "apiVersion" => "v1",
      "kind" => "Secret",
      "type" => "Opaque",
      "metadata" => %{"name" => @secret, "namespace" => @namespace, "labels" => labels()},
      "data" => Map.new(variables, fn {k, v} -> {k, Base.encode64(v)} end)
    }

  defp service_objects(service, commit, version, database) do
    selector = %{@service_label => service.name}

    deployment = %{
      "apiVersion" => "apps/v1",
      "kind" => "Deployment",
      "metadata" => %{
        "name" => service.name,
        "namespace" => @namespace,
        "labels" => Map.merge(labels(), selector)
      },
      "spec" => %{
        "replicas" => service.replicas,
        "strategy" => strategy(service.strategy),
        "selector" => %{"matchLabels" => selector},
        "template" => %{
          "metadata" => %{
            "labels" => Map.merge(labels(), selector),
            "annotations" =>
              put_if(
                %{"nvoi.to/commit" => commit, "nvoi.to/variables" => version},
                database != nil,
                "nvoi.to/database",
                database
              )
          },
          "spec" => %{
            "enableServiceLinks" => false,
            "containers" => [container(service, commit)]
          }
        }
      }
    }

    if service.port do
      [
        deployment,
        %{
          "apiVersion" => "v1",
          "kind" => "Service",
          "metadata" => %{
            "name" => service.name,
            "namespace" => @namespace,
            "labels" => Map.merge(labels(), selector)
          },
          "spec" => %{
            "selector" => selector,
            "ports" => [%{"name" => "main", "port" => service.port, "targetPort" => service.port}]
          }
        }
      ]
    else
      [deployment]
    end
  end

  @doc "The service's container: its image, command, variables, port and resources."
  def container(service, commit) do
    own = service.image in [nil, ""]

    %{
      "name" => service.name,
      "image" => if(own, do: Nvoi.Image.name(commit), else: service.image),
      "imagePullPolicy" => if(own, do: "Never", else: "IfNotPresent"),
      "envFrom" => [%{"secretRef" => %{"name" => @secret}}]
    }
    |> put_if(service.command not in [nil, ""], "command", ["sh", "-c", service.command])
    |> put_if(service.port != nil, "ports", [%{"containerPort" => service.port}])
    |> put_if(resources(service) != %{}, "resources", %{
      "requests" => resources(service),
      "limits" => resources(service)
    })
  end

  defp resources(service) do
    %{}
    |> put_if(service.cpu not in [nil, ""], "cpu", service.cpu)
    |> put_if(service.memory not in [nil, ""], "memory", service.memory)
  end

  # Recreate: the old pods are gone before a new one starts. RollingUpdate: one new pod before an old one goes.
  defp strategy("Recreate"), do: %{"type" => "Recreate"}

  defp strategy(_),
    do: %{"type" => "RollingUpdate", "rollingUpdate" => %{"maxSurge" => 1, "maxUnavailable" => 0}}

  defp labels, do: Map.new([@managed])
  defp put_if(map, true, key, value), do: Map.put(map, key, value)
  defp put_if(map, false, _key, _value), do: map
end
