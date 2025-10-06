defmodule Dagger.Codegen do
  @moduledoc """
  Functions for generating code from Dagger GraphQL.
  """

  alias Dagger.Codegen.Introspection.Types.Schema

  def generate(generator, introspection_schema) do
    visit(introspection_schema, fn type ->
      code = do_generate(type, generator)
      {generator.filename(type), generator.format(code)}
    end)
  end

  defp visit(%Schema{types: types}, generate) do
    types
    |> Stream.reject(&graphql_primitive_types/1)
    |> Stream.map(&modify_type/1)
    |> Task.async_stream(&generate.(&1), ordered: false)
  end

  defp modify_type(type) do
    %{
      type
      | fields: maybe_sort_fields(type.fields),
        input_fields: maybe_sort_fields(type.input_fields)
    }
  end

  defp maybe_sort_fields(nil), do: nil
  defp maybe_sort_fields(fields), do: Enum.sort_by(fields, & &1.name)

  defp graphql_primitive_types(type) do
    String.starts_with?(type.name, "_") or
      type.name in ["String", "Float", "Int", "Boolean", "DateTime", "ID"]
  end

  defp do_generate(%{kind: "SCALAR"} = type, generator) do
    generator.generate_scalar(type)
  end

  defp do_generate(%{kind: "INPUT_OBJECT"} = type, generator) do
    generator.generate_input(type)
  end

  defp do_generate(%{kind: "OBJECT"} = type, generator) do
    generator.generate_object(type)
  end

  defp do_generate(%{kind: "ENUM"} = type, generator) do
    generator.generate_enum(type)
  end
end
