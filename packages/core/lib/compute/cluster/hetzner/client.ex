defmodule Compute.Cluster.Hetzner.Client do
  @moduledoc """
  Hetzner Cloud's API (https://api.hetzner.cloud/v1), the key a project's API token. 429 and 5xx are retried with
  backoff; anything else not 2xx raises `Compute.Error` with the API's error code in the message. Tests answer it
  through `config :nvoi, Compute, hetzner_req: [plug: …]`.
  """
  @base "https://api.hetzner.cloud/v1"
  @retried [429, 500, 502, 503, 504]
  @attempts 5
  # Hetzner's largest page.
  @per_page 50
  # How often and how long an action is polled: server creation takes about 30 s, a volume resize a few.
  @poll_ms 2000
  @action_timeout_ms 600_000

  def get(key, path, params \\ []), do: request(key, :get, path, params: params)
  def post(key, path, body \\ %{}), do: request(key, :post, path, json: body)
  def delete(key, path), do: request(key, :delete, path, [])

  @doc "The one item of `collection` named `name`, or nil."
  def find(key, collection, name),
    do: key |> get("/#{collection}", name: name) |> Map.fetch!(collection) |> List.first()

  @doc "Every item of `collection` carrying all of `labels`, every page read."
  def list(key, collection, labels, page \\ 1) do
    body =
      get(key, "/#{collection}",
        label_selector: selector(labels),
        per_page: @per_page,
        page: page
      )

    items = Map.fetch!(body, collection)

    case get_in(body, ["meta", "pagination", "next_page"]) do
      nil -> items
      next -> items ++ list(key, collection, labels, next)
    end
  end

  def selector(labels),
    do: labels |> Enum.sort() |> Enum.map_join(",", fn {k, v} -> "#{k}=#{v}" end)

  @doc "Waits for each action in a response (`action`, `actions`, `next_actions`) to succeed; raises on its error."
  def wait(key, body) when is_map(body) do
    actions =
      List.wrap(body["action"]) ++ List.wrap(body["actions"]) ++ List.wrap(body["next_actions"])

    Enum.each(
      actions,
      &wait_action(key, &1, System.monotonic_time(:millisecond) + @action_timeout_ms)
    )

    body
  end

  defp wait_action(_key, %{"status" => "success"}, _until), do: :ok

  defp wait_action(_key, %{"status" => "error"} = action, _until),
    do:
      raise(Compute.Error,
        message:
          "hetzner #{action["command"]} #{action["id"]}: #{get_in(action, ["error", "message"])}"
      )

  defp wait_action(key, action, until) do
    if System.monotonic_time(:millisecond) > until,
      do:
        raise(Compute.Error,
          message: "hetzner #{action["command"]} #{action["id"]} did not finish",
          transient: true
        )

    Process.sleep(@poll_ms)
    wait_action(key, get(key, "/actions/#{action["id"]}")["action"], until)
  end

  @doc "The error code Hetzner answered (`uniqueness_error`, `resource_in_use`, `not_found`…), from a raised error."
  def code(%Compute.Error{message: message}), do: message |> String.split(" ") |> List.last()

  defp request(key, method, path, opts, attempt \\ 1) do
    req =
      [
        method: method,
        url: @base <> path,
        auth: {:bearer, key},
        retry: false,
        receive_timeout: 60_000
      ] ++
        opts ++ Compute.env(:hetzner_req, [])

    case Req.request(req) do
      {:ok, %{status: status, body: body}} when status in 200..299 ->
        if body == "", do: %{}, else: body

      {:ok, %{status: status}} when status in @retried and attempt < @attempts ->
        Process.sleep(attempt * 1000)
        request(key, method, path, opts, attempt + 1)

      {:ok, %{status: status, body: body}} ->
        error =
          if is_map(body),
            do: body["error"] || %{},
            else: %{"message" => body |> to_string() |> String.slice(0, 200)}

        raise Compute.Error,
          message:
            "hetzner #{String.upcase(to_string(method))} #{path} #{status}: #{error["message"]} #{error["code"]}",
          status: status,
          transient: status in @retried

      {:error, _} when attempt < @attempts ->
        Process.sleep(attempt * 1000)
        request(key, method, path, opts, attempt + 1)

      {:error, e} ->
        raise Compute.Error,
          message: "hetzner unreachable: #{Exception.message(e)}",
          transient: true
    end
  end
end
