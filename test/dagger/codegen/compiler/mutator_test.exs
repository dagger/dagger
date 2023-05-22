defmodule Dagger.Codegen.Compiler.MutatorTest do
  use ExUnit.Case, async: true

  alias Dagger.Codegen.Compiler.Mutator

  describe "mutate/1" do
    test "generate module name" do
      type = %{
        "name" => "GitRef",
        "kind" => "OBJECT",
        "inputFields" => nil,
        "interfaces" => [],
        "fields" => []
      }

      %{"private" => %{mod_name: Dagger.GitRef}} = Mutator.mutate(type)
    end

    test "rename Query type into Client" do
      type = %{
        "name" => "Query",
        "kind" => "OBJECT",
        "inputFields" => nil,
        "interfaces" => [],
        "fields" => []
      }

      %{"name" => "Client", "private" => %{mod_name: Dagger.Client}} = Mutator.mutate(type)
    end
  end
end
