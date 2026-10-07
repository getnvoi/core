defmodule NvoiWeb do
  @moduledoc """
  The entrypoint for defining your web interface, such
  as controllers, components, channels, and so on.

  This can be used in your application as:

      use NvoiWeb, :controller
      use NvoiWeb, :html

  The definitions below will be executed for every controller,
  component, etc, so keep them short and clean, focused
  on imports, uses and aliases.

  Do NOT define functions inside the quoted expressions
  below. Instead, define additional modules and import
  those modules here.
  """

  def static_paths, do: ~w(assets fonts images favicon.ico robots.txt)

  def router do
    quote do
      use Phoenix.Router, helpers: false

      # Import common connection and controller functions to use in pipelines
      import Plug.Conn
      import Phoenix.Controller
    end
  end

  def channel do
    quote do
      use Phoenix.Channel
    end
  end

  def controller do
    quote do
      use Phoenix.Controller, formats: [:html, :json]

      import Plug.Conn

      unquote(verified_routes())
    end
  end

  @doc "The router the installation serves (NvoiWeb.Endpoint): its `tools/0` are every routed tool."
  def served_router, do: Application.get_env(:nvoi, :router, NvoiWeb.Router)

  @doc """
  The installation's mode, which the sign-in and whoami answer: `selfhosted` for the core alone, else what the
  installation's config sets (`config :nvoi, :mode`). The API document marks the operations the core does not serve
  with it (`x-nvoi-mode`), and the CLI hides them on a context of another mode.
  """
  def mode, do: Application.get_env(:nvoi, :mode, "selfhosted")

  def verified_routes do
    quote do
      use Phoenix.VerifiedRoutes,
        endpoint: NvoiWeb.Endpoint,
        router: NvoiWeb.Router,
        statics: NvoiWeb.static_paths()
    end
  end

  @doc """
  When used, dispatch to the appropriate controller/live_view/etc.
  """
  defmacro __using__(which) when is_atom(which) do
    apply(__MODULE__, which, [])
  end
end
