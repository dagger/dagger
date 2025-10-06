defmodule Dagger.Codegen.ElixirGenerator.ScalarRenderer do
  @moduledoc """
  Provides functions to render small part of Elixir scalar code.
  """

  alias Dagger.Codegen.ElixirGenerator.Renderer

  @doc """
  Render scalar type into module. 
  """
  def render(type) do
    Renderer.render_module(type, [
      """
      use Dagger.Core.Base, kind: :scalar, name: "#{type.name}"
      """,
      ?\n,
      ?\n,
      "@type t() :: String.t()"
    ])
  end
end
