defmodule Dagger.Core.QueryBuilderTest do
  use ExUnit.Case, async: true

  alias Dagger.Core.QueryBuilder, as: QB

  describe "build/1" do
    test "encode atom to enum" do
      q =
        QB.query()
        |> QB.select("container")
        |> QB.select("withExposedPort")
        |> QB.put_arg("protocol", :TCP)
        |> QB.build()

      assert q == "query{container{withExposedPort(protocol:TCP)}}"
    end
  end
end
