defmodule Dagger.Codegen do
  @moduledoc """
  Functions for generating code from Dagger GraphQL.
  """

  alias Dagger.Codegen.Introspection.Types.Schema

  @nullable_objects_version Version.parse!("1.0.0-beta.10")
  # The first schema version whose bindings load every ID-returning field that
  # carries an @expectedType directive as the object it names. Older views only
  # convert fields returning their parent's own ID (the sync-like shape).
  @id_handles_version Version.parse!("1.0.0-beta.12")

  def generate(generator, introspection_schema) do
    type_features = %{
      supports_nullable_objects:
        schema_version_at_least?(introspection_schema.version, @nullable_objects_version),
      supports_id_handles:
        schema_version_at_least?(introspection_schema.version, @id_handles_version)
    }

    visit(introspection_schema, fn type ->
      code = do_generate(struct!(type, type_features), generator)
      {generator.filename(type), generator.format(code)}
    end)
  end

  # Compare a schema version against a feature cutover. Unknown versions
  # (development builds) get every feature; a beta prerelease is compared by
  # its beta number, ignoring any dev suffix.
  defp schema_version_at_least?(nil, _cutover), do: true

  defp schema_version_at_least?(version, cutover) do
    version = String.trim_leading(version, "v")

    version =
      case Regex.run(~r/^\d+\.\d+\.\d+-beta\.\d+/, version) do
        [beta_version] -> beta_version
        nil -> version
      end

    case Version.parse(version) do
      {:ok, version} -> Version.compare(version, cutover) != :lt
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
