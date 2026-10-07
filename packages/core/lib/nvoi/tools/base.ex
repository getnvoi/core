defmodule Nvoi.Tools.Base do
  @moduledoc """
  One of nvoi's own tools an agent calls, performed here with credentials the box never holds. `call/2` takes the
  call's input and `%{session, turn, worktree}` and answers the text the agent reads, or raises `Nvoi.Tools.Error`
  with the agent's text and `plain`, the cause and fix said to a person.
  """
  @callback call(input :: map, context :: map) :: String.t()
  @callback description(worktree :: struct) :: String.t()
  @doc "Lines the tool adds to the agent's system prompt; none by default."
  @callback instructions(worktree :: struct | nil) :: String.t() | nil

  defmacro __using__(opts) do
    quote do
      @behaviour Nvoi.Tools.Base
      def tool_name, do: unquote(opts[:name])
      def icon, do: unquote(opts[:icon])
      @doc "The input that is the call, drawn as its row's one line."
      def target, do: unquote(opts[:target])
      @doc "Where the lists of tools file it (`Nvoi.Tools.Category`)."
      def category, do: unquote(opts[:category])

      def label,
        do:
          unquote(opts[:label] || opts[:name] |> String.replace("_", " ") |> String.capitalize())

      def done, do: unquote(opts[:done] || "#{opts[:label] || opts[:name]} done")
      def internal, do: unquote(Keyword.get(opts, :internal, false))
      def asks?, do: unquote(Keyword.get(opts, :asks, false))
      def box?, do: unquote(Keyword.get(opts, :box, false))
      def browser?, do: unquote(Keyword.get(opts, :browser, false))
      def outbound?, do: unquote(Keyword.get(opts, :outbound, false))
      def permissions, do: unquote(Macro.escape(opts[:permissions]))
      def schema, do: %{type: "object", properties: %{}, required: []}
      def instructions(_worktree), do: nil
      defoverridable schema: 0, instructions: 1
    end
  end
end

defmodule Nvoi.Tools.Error do
  defexception [:message, :plain]
  def plain(%{plain: nil, message: m}), do: String.capitalize(m)
  def plain(%{plain: p}), do: p
end
