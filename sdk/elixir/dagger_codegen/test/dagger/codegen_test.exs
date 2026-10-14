defmodule Dagger.CodegenTest do
  use ExUnit.Case
  doctest Dagger.Codegen

  alias Dagger.Codegen.Introspection.Types.Schema
  alias Dagger.Codegen.Naming

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

  describe "formatted names" do
    setup do
      read = fn path -> path |> File.read!() |> JSON.decode!() end

      %{
        json: read.("test/fixtures/schemas/formatted-names.json"),
        names: read.("test/fixtures/schemas/formatted-names.names.json")
      }
    end

    test "format Elixir names, leaving API names as the schema has them", %{
      json: json,
      names: names
    } do
      files = generate(json, names)

      assert Enum.sort(Map.keys(files)) ==
               ["client.ex", "commit_set.ex", "http_client.ex", "http_header.ex", "llm_id.ex"]

      client = files["client.ex"]
      assert client =~ "def http_client(%__MODULE__{} = client, base_url, optional_args \\\\ [])"
      assert client =~ "{:source_shas, [String.t()]}"
      assert client =~ ~s[QB.select("httpClient")]
      assert client =~ ~s[QB.put_arg("baseURL", base_url)]
      assert client =~ ~s|QB.maybe_put_arg("sourceSHAs", optional_args[:source_shas])|
      assert client =~ "%Dagger.HTTPClient{"

      http_client = files["http_client.ex"]
      assert http_client =~ "defmodule Dagger.HTTPClient do"
      assert http_client =~ ~s[use Dagger.Core.Base, kind: :object, name: "HTTPClient"]
      assert http_client =~ "def tls?(%__MODULE__{} = http_client)"
      assert http_client =~ "def prerequisite_shas(%__MODULE__{} = http_client)"
      assert http_client =~ ~s[QB.select("prerequisiteSHAs")]

      # The legacy name stays, deprecated.
      assert http_client =~ ~s[@deprecated "Use prerequisite_shas/1 instead"]
      assert http_client =~ "def prerequisite_sh_as(%__MODULE__{} = http_client)"
      refute http_client =~ "def is_tls"

      commit_set = files["commit_set.ex"]
      assert commit_set =~ "@type t() :: :PARENT_SHAS | :ParentSHAs"
      assert commit_set =~ "def parent_shas(), do: :PARENT_SHAS"
      assert commit_set =~ ~s[def from_string("ParentSHAs"), do: :ParentSHAs]
      assert commit_set =~ ~s[@deprecated "Use parent_shas/0 instead"]
      assert commit_set =~ "def parent_sh_as(), do: parent_shas()"

      # Input struct keys are sent to the API as is: they keep the legacy
      # conversion.
      assert files["http_header.ex"] =~ "defstruct [:name, :source_sh_as]"

      assert files["llm_id.ex"] =~ "defmodule Dagger.LLMID do"
    end

    test "fall back to the legacy conversion without names", %{json: json, names: names} do
      without_format_identifiers = update_in(json, ["__schema", "types"], &drop_format_identifiers/1)

      # no names, names with no formats, or names for a schema without
      # Query.formatIdentifiers
      for {json, names} <- [{json, nil}, {json, %{}}, {without_format_identifiers, names}] do
        files = generate(json, names)

        assert "llmid.ex" in Map.keys(files)
        refute "llm_id.ex" in Map.keys(files)

        assert files["client.ex"] =~
                 ~s|QB.maybe_put_arg("sourceSHAs", optional_args[:source_sh_as])|

        http_client = files["http_client.ex"]
        assert http_client =~ "def prerequisite_sh_as(%__MODULE__{} = http_client)"
        refute http_client =~ "prerequisite_shas"
        refute http_client =~ "@deprecated"

        commit_set = files["commit_set.ex"]
        assert commit_set =~ "def parent_shas(), do: :PARENT_SHAS"
        assert commit_set =~ "def parent_sh_as(), do: :ParentSHAs"
        refute commit_set =~ "@deprecated"
      end
    end

    test "fall back to the legacy conversion for names without an entry", %{
      json: json,
      names: names
    } do
      names = update_in(names, ["SNAKE:UPPERCASE"], &Map.delete(&1, "prerequisiteSHAs"))
      http_client = generate(json, names)["http_client.ex"]

      assert http_client =~ "def prerequisite_sh_as(%__MODULE__{} = http_client)"
      refute http_client =~ "prerequisite_shas"
      assert http_client =~ "def tls?(%__MODULE__{} = http_client)"
    end
  end

  defp drop_format_identifiers(types) do
    Enum.map(types, fn
      %{"name" => "Query"} = query ->
        Map.update!(query, "fields", fn fields ->
          Enum.reject(fields, &(&1["name"] == "formatIdentifiers"))
        end)

      type ->
        type
    end)
  end

  defp generate(json, names) do
    schema = json |> Schema.from_map() |> Schema.put_names(Naming.from_map(names))

    Dagger.Codegen.generate(Dagger.Codegen.ElixirGenerator, schema)
    |> Map.new(fn {:ok, {file, code}} -> {file, IO.iodata_to_binary(code)} end)
  end
end
