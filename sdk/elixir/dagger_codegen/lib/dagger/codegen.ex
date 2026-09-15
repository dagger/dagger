defmodule Dagger.Codegen do
  @moduledoc """
  Functions for generating code from Dagger GraphQL.
  """

  alias Dagger.Codegen.Introspection.Types.Schema

  @nullable_objects_version Version.parse!("1.0.0-beta.10")

  def generate(generator, introspection_schema) do
    supports_nullable_objects = supports_nullable_objects?(introspection_schema.version)

    visit(introspection_schema, fn type ->
      code = do_generate(%{type | supports_nullable_objects: supports_nullable_objects}, generator)
      {generator.filename(type), generator.format(code)}
    end)
  end

  defp supports_nullable_objects?(nil), do: true

  defp supports_nullable_objects?(version) do
    version = String.trim_leading(version, "v")

    version =
      case Regex.run(~r/^\d+\.\d+\.\d+-beta\.\d+/, version) do
        [beta_version] -> beta_version
        nil -> version
      end

    case Version.parse(version) do
      {:ok, version} -> Version.compare(version, @nullable_objects_version) != :lt
      :error -> true
    end
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

  defp do_generate(%{kind: "INTERFACE"} = type, generator) do
    generator.generate_object(type)
  end

  defp do_generate(%{kind: "ENUM"} = type, generator) do
    generator.generate_enum(type)
  end
end
