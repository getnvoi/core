defmodule Llm.Login do
  @moduledoc """
  What a provider's login (`Llm.Provider.login/1`) shares: the failures a person is told about, and the one HTTP
  client that talks to a vendor's login server. A login is `start` (a code to show), `poll` (not yet, or a grant)
  and `exchange` (the grant for tokens); each raises `Llm.Login.Error` with a kind and a sentence.
  """

  defmodule Error do
    @moduledoc """
    `kind` is :disabled (the account has the device login off), :expired, :denied, :refused (the vendor answered
    no) or :unreachable (it did not answer).
    """
    @kinds [:disabled, :expired, :denied, :refused, :unreachable]
    defexception [:kind, :message]

    def new(kind, message) when kind in @kinds, do: %__MODULE__{kind: kind, message: message}
  end

  defmodule Code do
    @moduledoc "What `start` answers: the code to show and when to look, with the page to enter it on."
    defstruct [:device_auth_id, :user_code, :interval, :expires_at, :verification_url]
  end

  defmodule Grant do
    @moduledoc "What `poll` answers once the person approved."
    defstruct [:authorization_code, :code_challenge, :code_verifier]
  end

  defmodule Tokens do
    @moduledoc "What `exchange` answers."
    defstruct [:id_token, :access_token, :refresh_token]
  end

  # How long a vendor has to accept the connection, then to answer.
  @connect_timeout 5_000
  @receive_timeout 15_000

  @doc """
  One POST: `{:json, map}` sends a JSON body, `{:form, map}` a form. Answers `{status, body}`, the body a map ({}
  when it is not JSON). A network failure raises :unreachable. `config :nvoi, Llm.Login` adds Req options (tests
  put a `Req.Test` plug there).
  """
  def post(url, {kind, body}) when kind in [:json, :form] do
    opts =
      [
        method: :post,
        url: url,
        headers: [{"accept", "application/json"}],
        retry: false,
        decode_body: false,
        connect_options: [timeout: @connect_timeout],
        receive_timeout: @receive_timeout
      ]
      |> Keyword.put(kind, body)
      |> Keyword.merge(Application.get_env(:nvoi, Llm.Login, []))

    case Req.request(opts) do
      {:ok, %{status: status, body: body}} ->
        {status, parse(body)}

      {:error, e} ->
        raise Error.new(
                :unreachable,
                "#{URI.parse(url).host} did not answer: #{Exception.message(e)}"
              )
    end
  end

  defp parse(body) do
    case Jason.decode(to_string(body)) do
      {:ok, %{} = map} -> map
      _ -> %{}
    end
  end

  @doc "Whether any of the struct's values is missing or empty."
  def incomplete?(struct),
    do: struct |> Map.from_struct() |> Map.values() |> Enum.any?(&(&1 in [nil, ""]))
end
