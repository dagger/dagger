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

  describe "identifier words" do
    setup do
      %{json: "test/fixtures/schemas/identifier-words.json" |> File.read!() |> JSON.decode!()}
    end

    test "format Elixir names, leaving API names as the schema has them", %{json: json} do
      files = generate(json)

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

    test "fall back to the legacy conversion without words", %{json: json} do
      files = json |> Map.delete("__identifiers") |> generate()

      assert Enum.sort(Map.keys(files)) ==
               ["client.ex", "commit_set.ex", "http_client.ex", "http_header.ex", "llmid.ex"]

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

  defp generate(json) do
    Dagger.Codegen.generate(Dagger.Codegen.ElixirGenerator, Schema.from_map(json))
    |> Map.new(fn {:ok, {file, code}} -> {file, IO.iodata_to_binary(code)} end)
  end
end
