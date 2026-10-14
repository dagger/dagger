defmodule Dagger.Codegen.NamingTest do
  use ExUnit.Case, async: true

  alias Dagger.Codegen.Naming

  test "formats/0 lists the generator's name formats" do
    assert Naming.formats() == ["PASCAL:UPPERCASE", "SNAKE:UPPERCASE"]
    assert Naming.key(:camel, :capitalized) == "CAMEL:CAPITALIZED"
  end

  test "format/3 looks up the process's names" do
    assert Naming.format("httpClient", :snake) == nil

    Naming.put_names(%{
      "SNAKE:UPPERCASE" => %{"httpClient" => "http_client"},
      "PASCAL:UPPERCASE" => %{"httpClient" => "HTTPClient"}
    })

    assert Naming.format("httpClient", :snake) == "http_client"
    assert Naming.format("httpClient", :snake, :uppercase) == "http_client"
    assert Naming.format("httpClient", :pascal, :uppercase) == "HTTPClient"
    # a format or name the engine didn't format
    assert Naming.format("httpClient", :pascal, :capitalized) == nil
    assert Naming.format("other", :snake) == nil

    Naming.put_names(nil)
    assert Naming.format("httpClient", :snake) == nil
  end

  test "from_map/1 decodes the names file" do
    assert Naming.from_map(nil) == nil
    assert Naming.from_map(%{}) == %{}

    assert Naming.from_map(%{
             "SNAKE:UPPERCASE" => %{"prerequisiteSHAs" => "prerequisite_shas", "bad" => 1, "empty" => ""},
             "PASCAL:UPPERCASE" => "not a map"
           }) == %{"SNAKE:UPPERCASE" => %{"prerequisiteSHAs" => "prerequisite_shas"}}
  end
end
