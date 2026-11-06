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

    test "encode input object with schema field names" do
      q =
        QB.query()
        |> QB.select("withOrigin")
        |> QB.put_arg("origin", %Dagger.LLMMessageOriginInput{
          agent_name: "bot",
          kind: :AGENT,
          reply_to: "msg-1"
        })
        |> QB.build()

      assert q ==
               ~s|query{withOrigin(origin:{agentName:"bot",kind:AGENT,ref:null,replyTo:"msg-1"})}|
    end
  end
end
