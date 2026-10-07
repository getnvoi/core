defmodule Nvoi.Tools.Browser do
  @moduledoc """
  A call in the session's preview: the page the app of a member shows beside the conversation, in its own browser
  view. The server never performs it: the call waits, like an approval, until an app showing the preview picks it
  up, performs it and answers (`Nvoi.Browser`). Its answer is what the agent reads.
  """

  @said "The page is the session's preview, open in the app of a member who follows this session; they see what " <>
          "you do. The call waits for that app and answers with the result and the page's state after it; it " <>
          "fails when nobody has the preview open."

  def said, do: @said

  defmacro __using__(opts) do
    quote do
      use Nvoi.Tools.Base,
          unquote(Keyword.merge([icon: "globe", browser: true], Keyword.delete(opts, :says)))

      def description(_), do: unquote(opts[:says]) <> " " <> Nvoi.Tools.Browser.said()

      def call(_input, _context),
        do: raise(Nvoi.Tools.Error, message: "the app showing the preview performs it")
    end
  end
end
