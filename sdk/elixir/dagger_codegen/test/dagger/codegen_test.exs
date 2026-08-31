defmodule Dagger.CodegenTest do
  use ExUnit.Case
  doctest Dagger.Codegen

  alias Dagger.Codegen.Introspection.Types.Schema

  defmodule VersionGenerator do
    def generate_object(type), do: type.supports_nullable_objects
    def filename(_type), do: "type"
    def format(value), do: value
  end

  test "reads the schema version" do
    schema =
      Schema.from_map(%{
        "__schemaVersion" => "v1.0.0-beta.9",
        "__schema" => %{
          "queryType" => %{"name" => "Query"},
          "types" => []
        }
      })

    assert schema.version == "v1.0.0-beta.9"
  end

  test "nullable object version gate handles boundaries and development versions" do
    for {version, expected} <- [
          {nil, true},
          {"development", true},
          {"v0.21.0-dev", false},
          {"v1.0.0-beta.9-dev", false},
          {"v1.0.0-beta.10", true},
          {"v1.0.0-beta.10-dev", true},
          {"v1.0.0-rc.1", true},
          {"v1.0.0", true}
        ] do
      schema =
        Schema.from_map(%{
          "__schemaVersion" => version,
          "__schema" => %{
            "queryType" => %{"name" => "Query"},
            "types" => [%{"kind" => "OBJECT", "name" => "Query"}]
          }
        })

      assert [ok: {"type", ^expected}] =
               Dagger.Codegen.generate(VersionGenerator, schema) |> Enum.to_list()
    end
  end
end
