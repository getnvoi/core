defmodule Compute.Database.Client do
  @moduledoc "A vendor's API; 429 and 5xx are retried a few times, a `busy` status waited out."
  @retried [429, 502, 503, 504]
  @tries 12

  def get(c, path), do: request(c, :get, path, nil)
  def post(c, path, body \\ nil), do: request(c, :post, path, body)
  def patch(c, path, body), do: request(c, :patch, path, body)

  def delete!(c, path) do
    request(c, :delete, path, nil)
  rescue
    e in Compute.Error -> if e.status == 404, do: nil, else: reraise(e, __STACKTRACE__)
  end

  defp request(c, method, path, body, tries \\ 1) do
    opts =
      [
        method: method,
        url: c.base <> path,
        auth: {:bearer, c.key},
        retry: false,
        receive_timeout: 120_000
      ] ++ if(body, do: [json: body], else: []) ++ Compute.env(:database_req, [])

    case Req.request(opts) do
      {:ok, %{status: s, body: b}} when s in 200..299 ->
        if b == "", do: %{}, else: b

      {:ok, %{status: s}} when tries < @tries and (s in [] or s in @retried) and tries < 4 ->
        Process.sleep(tries * 500) && request(c, method, path, body, tries + 1)

      {:ok, %{status: s}} = r when tries < @tries ->
        if s in Map.get(c, :busy, []),
          do: Process.sleep(5000) && request(c, method, path, body, tries + 1),
          else: fail(c, method, path, r)

      {:ok, _} = r ->
        fail(c, method, path, r)

      {:error, e} ->
        raise Compute.Error,
          message: "#{c.vendor} unreachable: #{Exception.message(e)}",
          transient: true
    end
  end

  defp fail(c, method, path, {:ok, %{status: s, body: b}}) do
    detail =
      if is_map(b),
        do: [b["code"], b["message"] || b["error"]] |> Enum.reject(&is_nil/1) |> Enum.join(" "),
        else: b |> to_string() |> String.slice(0, 200)

    raise Compute.Error,
      message: "#{c.vendor} #{String.upcase(to_string(method))} #{path} #{s}: #{detail}",
      status: s
  end
end
