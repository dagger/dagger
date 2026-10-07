defmodule Dagger.Codegen.NamingTest do
  use ExUnit.Case, async: true

  alias Dagger.Codegen.Naming

  # The engine's shared test vectors. The dev container mounts the file and
  # points DAGGER_NAMING_VECTORS at it; in a repository checkout it is found
  # relative to this file.
  @checkout_vectors_path Path.expand(
                           "../../../../../../engine/naming/testdata/vectors.json",
                           __DIR__
                         )

  defp vectors_path, do: System.get_env("DAGGER_NAMING_VECTORS") || @checkout_vectors_path

  @formats %{
    "PASCAL" => {:pascal, :uppercase},
    "PASCAL_CAPITALIZED" => {:pascal, :capitalized},
    "CAMEL" => {:camel, :uppercase},
    "CAMEL_CAPITALIZED" => {:camel, :capitalized},
    "SNAKE" => {:snake, :uppercase},
    "SCREAMING_SNAKE" => {:screaming_snake, :uppercase},
    "KEBAB" => {:kebab, :uppercase},
    "FLAT" => {:flat, :uppercase}
  }

  test "formats the engine's test vectors" do
    vectors = vectors_path() |> File.read!() |> JSON.decode!()
    assert vectors != []

    for %{"input" => input, "words" => words, "formats" => formats} <- vectors,
        {format, expected} <- formats do
      {casing, acronyms} = Map.fetch!(@formats, format)
      words = Enum.map(words, &Naming.word_from_map/1)

      assert {input, format, Naming.format(words, casing, acronyms)} ==
               {input, format, expected}
    end
  end

  test "words/1 looks up the process's identifiers" do
    assert Naming.words("httpClient") == nil

    words = [
      %{kind: "ACRONYM", text: "HTTP", suffix: "", capitalized: "Http"},
      %{kind: "WORD", text: "client", suffix: "", capitalized: "Client"}
    ]

    Naming.put_identifiers(%{"httpClient" => words, "empty" => []})
    assert Naming.words("httpClient") == words
    assert Naming.words("empty") == nil
    assert Naming.words("other") == nil

    Naming.put_identifiers(nil)
    assert Naming.words("httpClient") == nil
  end

  test "from_map/1 decodes the schema JSON words" do
    assert Naming.from_map(nil) == nil

    assert Naming.from_map(%{
             "prerequisiteSHAs" => [
               %{
                 "kind" => "WORD",
                 "text" => "prerequisite",
                 "suffix" => "",
                 "capitalized" => "Prerequisite"
               },
               %{"kind" => "ACRONYM", "text" => "SHA", "suffix" => "s", "capitalized" => "Sha"}
             ]
           }) == %{
             "prerequisiteSHAs" => [
               %{kind: "WORD", text: "prerequisite", suffix: "", capitalized: "Prerequisite"},
               %{kind: "ACRONYM", text: "SHA", suffix: "s", capitalized: "Sha"}
             ]
           }
  end
end
