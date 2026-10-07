defmodule Compute.SandboxVm.Daytona.Client do
  @moduledoc "Daytona's API; 429 and 5xx are retried with backoff unless `again: false`."
  @base "https://app.daytona.io/api"
  @analytics "https://analytics.app.daytona.io"
  @retried [429, 500, 502, 503, 504]
  @attempts 5

  def get(key, path, params \\ []), do: request(key, :get, path, params: params)

  def post(key, path, body \\ %{}, again \\ true),
    do: request(key, :post, path, json: body, again: again)

  def put(key, path, body), do: request(key, :put, path, json: body)
  def delete(key, path, params \\ []), do: request(key, :delete, path, params: params)
  def analytics(key, path, params), do: request(key, :get, path, params: params, base: @analytics)

  def find(key, path) do
    get(key, path)
  rescue
    e in Compute.Error -> if e.status == 404, do: nil, else: reraise(e, __STACKTRACE__)
  end

  defp request(key, method, path, opts, attempt \\ 1) do
    {again, opts} = Keyword.pop(opts, :again, true)
    {base, opts} = Keyword.pop(opts, :base, @base)

    req =
      [
        method: method,
        url: base <> path,
        auth: {:bearer, key},
        retry: false,
        receive_timeout: 120_000
      ] ++ opts

    case Req.request(req) do
      {:ok, %{status: status, body: body}} when status in 200..299 ->
        body

      {:ok, %{status: status}} when again and status in @retried and attempt < @attempts ->
        Process.sleep(attempt * 1000)
        request(key, method, path, [again: again, base: base] ++ opts, attempt + 1)

      {:ok, %{status: status, body: body}} ->
        detail =
          if is_map(body), do: body["message"], else: body |> to_string() |> String.slice(0, 200)

        raise Compute.Error,
          message: "daytona #{String.upcase(to_string(method))} #{path} #{status}: #{detail}",
          status: status,
          transient: status in @retried

      {:error, _} when again and attempt < @attempts ->
        Process.sleep(attempt * 1000)
        request(key, method, path, [again: again, base: base] ++ opts, attempt + 1)

      {:error, e} ->
        raise Compute.Error,
          message: "daytona unreachable: #{Exception.message(e)}",
          transient: true
    end
  end
end
