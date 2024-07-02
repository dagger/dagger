defmodule Dagger.Codegen.ElixirGenerator.InputRenderer do
  @moduledoc """
  Provides functions to render small part of Elixir input code.
  """

  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.ElixirGenerator.Renderer

  @doc """
  Render input type into module. 
  """
  def render(type) do
    Renderer.render_module(type, render_module_body(type))
  end

  def render_module_body(type) do
    [
      "@type t() :: %__MODULE__{",
      Enum.map_intersperse(type.input_fields, ",", &render_struct_field/1),
      ?\n,
      "}",
      ?\n,
      ?\n,
      "defstruct [",
      Enum.map_intersperse(
        type.input_fields,
        ",",
        &(&1.name |> Formatter.format_var_name() |> Renderer.render_atom())
      ),
      "]"
    ]
  end

  def render_struct_field(input_field) do
    var_name = Formatter.format_var_name(input_field.name)
    type = Formatter.format_type(input_field.type)

    [var_name, ": ", type]
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
end
