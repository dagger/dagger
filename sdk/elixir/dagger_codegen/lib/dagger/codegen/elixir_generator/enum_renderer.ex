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
    unique_enum_values = Enum.uniq_by(type.enum_values, &Formatter.format_function_name(&1.name))
    [
      """
      use Dagger.Core.Base, kind: :enum, name: "#{type.name}"
      """,
      ?\n,
      ?\n,
      "@type t() :: ",
      type.enum_values
      |> Enum.map(&Renderer.render_atom(&1.name))
      |> render_union_type(),
      ?\n,
      ?\n,
      for enum_value <- unique_enum_values do
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
      ?\n,
      render_legacy_aliases(type.enum_values)
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

  @doc """
  Render deprecated aliases for the functions whose name changed when it was
  formatted from the schema's identifier words, under their legacy names.
  Renders nothing for schemas without words, or when a legacy name is taken.
  """
  def render_legacy_aliases(enum_values) do
    names = MapSet.new(enum_values, &Formatter.format_function_name(&1.name))

    enum_values
    |> Enum.map(fn %{name: name} ->
      {Formatter.format_function_name(name), Formatter.legacy_function_name(name)}
    end)
    |> Enum.reject(fn {_fun_name, legacy_name} -> MapSet.member?(names, legacy_name) end)
    |> Enum.uniq_by(fn {_fun_name, legacy_name} -> legacy_name end)
    |> Enum.map(fn {fun_name, legacy_name} ->
      """
      @doc false
      @deprecated "Use #{fun_name}/0 instead"
      def #{legacy_name}(), do: #{fun_name}()

      """
    end)
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
