defmodule Dagger.Codegen.ElixirGenerator do
  @moduledoc """
  Dagger Elixir code generator.
  """

  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.Introspection.Types.InputValue
  alias Dagger.Codegen.Introspection.Types.TypeRef
  alias Dagger.Codegen.Introspection.Visitor
  alias Dagger.Codegen.Introspection.VisitorHandlers

  require EEx

  @template_dir Path.join([:code.priv_dir(:dagger_codegen), "templates", "elixir"])

  @scalar_template Path.join(@template_dir, "scalar.eex")
  @object_template Path.join(@template_dir, "object.eex")
  @input_template Path.join(@template_dir, "input.eex")
  @enum_template Path.join(@template_dir, "enum.eex")

  EEx.function_from_file(:defp, :scalar_template, @scalar_template, [:assigns])
  EEx.function_from_file(:defp, :object_template, @object_template, [:assigns])
  EEx.function_from_file(:defp, :input_template, @input_template, [:assigns])
  EEx.function_from_file(:defp, :enum_template, @enum_template, [:assigns])

  def generate(schema) do
    generate_code(schema)
  end

  defp generate_code(schema) do
    handlers = %VisitorHandlers{
      scalar: fn type ->
        {"#{Formatter.format_var_name(type.name)}.ex", scalar_template(%{type: type})}
      end,
      object: fn type ->
        {"#{Formatter.format_var_name(type.name)}.ex",
         object_template(%{type: type, schema: schema})}
      end,
      input: fn type ->
        {"#{Formatter.format_var_name(type.name)}.ex", input_template(%{type: type})}
      end,
      enum: fn type ->
        {"#{Formatter.format_var_name(type.name)}.ex", enum_template(%{type: type})}
      end
    }

    Visitor.visit(schema, handlers)
  end
end
