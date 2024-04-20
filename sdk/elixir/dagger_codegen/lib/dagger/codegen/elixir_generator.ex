defmodule Dagger.Codegen.ElixirGenerator do
  @moduledoc """
  Dagger Elixir code generator.
  """

  alias Dagger.Codegen.ElixirGenerator.EnumRenderer
  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.ElixirGenerator.InputRenderer
  alias Dagger.Codegen.ElixirGenerator.ObjectRenderer
  alias Dagger.Codegen.ElixirGenerator.ScalarRenderer
  alias Dagger.Codegen.Introspection.Visitor
  alias Dagger.Codegen.Introspection.VisitorHandlers

  def generate(schema) do
    generate_code(schema)
  end

  defp generate_code(schema) do
    handlers = %VisitorHandlers{
      scalar: fn type ->
        {"#{Formatter.format_var_name(type.name)}.ex", ScalarRenderer.render(type)}
      end,
      object: fn type ->
        {"#{Formatter.format_var_name(type.name)}.ex", ObjectRenderer.render(type)}
      end,
      input: fn type ->
        {"#{Formatter.format_var_name(type.name)}.ex", InputRenderer.render(type)}
      end,
      enum: fn type ->
        {"#{Formatter.format_var_name(type.name)}.ex", EnumRenderer.render(type)}
      end
    }

    Visitor.visit(schema, handlers)
  end
end
