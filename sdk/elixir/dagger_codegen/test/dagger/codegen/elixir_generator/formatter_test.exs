defmodule Dagger.Codegen.ElixirGenerator.FormatterTest do
  use ExUnit.Case, async: true
  use ExUnitProperties

  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.Introspection.Types.TypeRef

  test "format_module/1" do
    assert Formatter.format_module("Container") == "Dagger.Container"
    assert Formatter.format_module("BuildArg") == "Dagger.BuildArg"
  end

  test "format_var_name/1" do
    assert Formatter.format_var_name("Container") == "container"
    assert Formatter.format_var_name("CacheVolume") == "cache_volume"
  end

  property "format_function_name/1" do
    assert Formatter.format_function_name("withEnvVariable") == "with_env_variable"
    assert Formatter.format_function_name("loadSecretFromID") == "load_secret_from_id"

    assert Formatter.format_function_name("experimentalWithAllGPUs") ==
             "experimental_with_all_gpus"

    assert Formatter.format_function_name("doOpenPR") ==
             "do_open_pr"

    assert Formatter.format_function_name("true") == "true_"
    assert Formatter.format_function_name("do") == "do_"

    check all words <- list_of(string([?a..?z, ?A..?Z], min_length: 2), min_length: 1) do
      [first | rest] = words

      actual = Enum.join([String.downcase(first) | Enum.map(rest, &acronym/1)], "")
      expected = Enum.map_join(words, "_", &String.downcase/1)

      assert Formatter.format_function_name(actual) == expected
    end
  end

  defp acronym(string) do
    n = String.length(string)
    size = :rand.uniform(n - 1)
    <<s::binary-size(size), rest::binary>> = String.downcase(string)
    [String.upcase(s), String.downcase(rest)]
  end

  test "format_doc/1" do
    assert Formatter.format_doc("A simple document") == "A simple document"

    assert Formatter.format_doc("A simple document that reference to `someFunction`") ==
             "A simple document that reference to `some_function`"
  end

  test "format_type/1" do
    type = %TypeRef{
      kind: "LIST",
      name: nil,
      of_type: %TypeRef{
        kind: "NON_NULL",
        name: nil,
        of_type: %TypeRef{kind: "OBJECT", name: "EnvVariable", of_type: nil}
      }
    }

    assert Formatter.format_type(type) == "[Dagger.EnvVariable.t()]"

    type = %TypeRef{
      kind: "NON_NULL",
      name: nil,
      of_type: %TypeRef{kind: "OBJECT", name: "EnvVariable", of_type: nil}
    }

    assert Formatter.format_type(type) == "Dagger.EnvVariable.t()"

    type = %TypeRef{kind: "OBJECT", name: "EnvVariable", of_type: nil}

    assert Formatter.format_type(type) == "Dagger.EnvVariable.t() | nil"

    type = %TypeRef{
      kind: "NON_NULL",
      name: nil,
      of_type: %TypeRef{
        kind: "LIST",
        name: nil,
        of_type: %TypeRef{
          kind: "NON_NULL",
          name: nil,
          of_type: %TypeRef{kind: "OBJECT", name: "EnvVariable", of_type: nil}
        }
      }
    }

    assert Formatter.format_type(type) == "[Dagger.EnvVariable.t()]"
  end

  test "format_typespec_output_type/1" do
    type = %TypeRef{kind: "NON_NULL", of_type: %TypeRef{kind: "SCALAR", name: "String"}}
    assert Formatter.format_typespec_output_type(type) == "{:ok, String.t()} | {:error, term()}"

    type = %TypeRef{kind: "NON_NULL", of_type: %TypeRef{kind: "OBJECT", name: "CacheVolume"}}
    assert Formatter.format_typespec_output_type(type) == "Dagger.CacheVolume.t()"

    type = %TypeRef{
      kind: "LIST",
      name: nil,
      of_type: %TypeRef{
        kind: "NON_NULL",
        name: nil,
        of_type: %TypeRef{kind: "SCALAR", name: "String", of_type: nil}
      }
    }

    assert Formatter.format_typespec_output_type(type) == "{:ok, [String.t()]} | {:error, term()}"

    type = %TypeRef{kind: "SCALAR", name: "String", of_type: nil}

    assert Formatter.format_typespec_output_type(type) ==
             "{:ok, String.t() | nil} | {:error, term()}"
  end
end
