defmodule Dagger.Codegen.ElixirGenerator.FormatterTest do
  use ExUnit.Case, async: true

  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.Introspection.Types.TypeRef
  alias Dagger.Codegen.Naming

  test "format_module/1" do
    assert Formatter.format_module("Container") == "Dagger.Container"
    assert Formatter.format_module("BuildArg") == "Dagger.BuildArg"
  end

  test "format_var_name/1" do
    assert Formatter.format_var_name("Container") == "container"
    assert Formatter.format_var_name("CacheVolume") == "cache_volume"
  end

  test "format_function_name/1" do
    assert Formatter.format_function_name("withEnvVariable") == "with_env_variable"
    assert Formatter.format_function_name("loadSecretFromID") == "load_secret_from_id"

    assert Formatter.format_function_name("experimentalWithAllGPUs") ==
             "experimental_with_all_gpus"

    assert Formatter.format_function_name("true") == "true_"
    assert Formatter.format_function_name("do") == "do_"
  end

  test "format_doc/1" do
    assert Formatter.format_doc("A simple document") == "A simple document"

    assert Formatter.format_doc("A simple document that reference to `someFunction`") ==
             "A simple document that reference to `some_function`"
  end

  describe "with formatted names" do
    # Each test runs in its own process, so the names don't leak.
    setup do
      Naming.put_names(%{
        "PASCAL:UPPERCASE" => %{
          "JSONValue" => "JSONValue",
          "LLMID" => "LLMID",
          "GitHubRepo" => "GitHubRepo"
        },
        "SNAKE:UPPERCASE" => %{
          "prerequisiteSHAs" => "prerequisite_shas",
          "experimentalWithAllGPUs" => "experimental_with_all_gpus",
          "isEmpty" => "is_empty",
          "do" => "do",
          "LLMID" => "llm_id",
          "GitHubRepo" => "github_repo"
        }
      })
    end

    test "format_module/1" do
      assert Formatter.format_module("JSONValue") == "Dagger.JSONValue"
      assert Formatter.format_module("LLMID") == "Dagger.LLMID"
      assert Formatter.format_module("GitHubRepo") == "Dagger.GitHubRepo"
      assert Formatter.format_module("Query") == "Dagger.Client"
      # Names without an entry keep the legacy conversion.
      assert Formatter.format_module("Container") == "Dagger.Container"
    end

    test "format_var_name/1" do
      assert Formatter.format_var_name("LLMID") == "llm_id"
      assert Formatter.format_var_name("GitHubRepo") == "github_repo"
      assert Formatter.format_var_name("Query") == "client"
      assert Formatter.format_var_name("CacheVolume") == "cache_volume"
    end

    test "legacy_var_name/1 ignores the names" do
      assert Formatter.legacy_var_name("LLMID") == "llmid"
    end

    test "format_function_name/1" do
      assert Formatter.format_function_name("prerequisiteSHAs") == "prerequisite_shas"

      assert Formatter.format_function_name("experimentalWithAllGPUs") ==
               "experimental_with_all_gpus"

      assert Formatter.format_function_name("isEmpty") == "empty?"
      assert Formatter.format_function_name("do") == "do_"
    end

    test "legacy_function_name/1 ignores the names" do
      assert Formatter.legacy_function_name("prerequisiteSHAs") == "prerequisite_sh_as"

      assert Formatter.legacy_function_name("experimentalWithAllGPUs") ==
               "experimental_with_all_gpus"
    end

    test "format_doc/1" do
      assert Formatter.format_doc("Use `prerequisiteSHAs` instead") ==
               "Use `prerequisite_shas` instead"
    end
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
