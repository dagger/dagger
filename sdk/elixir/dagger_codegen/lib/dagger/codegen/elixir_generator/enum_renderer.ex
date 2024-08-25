defmodule Dagger.Codegen.ElixirGenerator.EnumRenderer do
  @moduledoc """
  Provides functions to render small part of Elixir enum code.
  """

  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.ElixirGenerator.Renderer

  @doc """
  Render enum type into module. 
  """
  def render(type) do
    Renderer.render_module(type, render_module_body(type))
  end

  def render_module_body(type) do
    [
      "@type t() :: ",
      type.enum_values
      |> Enum.map(&Renderer.render_atom(&1.name))
      |> render_union_type(),
      ?\n,
      ?\n,
      for enum_value <- type.enum_values do
        [
          render_function(enum_value),
          ?\n,
          ?\n
        ]
      end,
      ?\n,
      ?\n,
      render_from_string_function(type.enum_values),
      ?\n,
      ?\n
    ]
  end

  def render_function(enum_value) do
    fun_name = Formatter.format_function_name(enum_value.name)
    return_value = Renderer.render_atom(enum_value.name)

    [
      Renderer.render_doc(enum_value),
      ?\n,
      "@spec #{fun_name}() :: #{return_value}",
      ?\n,
      "def #{fun_name}(), do: #{return_value}"
    ]
  end

  def render_from_string_function(enum_values) do
    [
      """
      @doc false
      @spec from_string(String.t()) :: t()
      def from_string(string)
      """,
      ?\n,
      ?\n,
      for %{name: name} <- enum_values do
        value = Renderer.render_atom(name)

        """
        def from_string("#{name}"), do: #{value}
        """
      end
    ]
  end

  @doc "Render possible values in typespec."
  def render_union_type(types), do: Enum.join(types, " | ")
end
