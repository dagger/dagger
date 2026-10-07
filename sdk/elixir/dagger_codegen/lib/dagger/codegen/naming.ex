defmodule Dagger.Codegen.Naming do
  @moduledoc """
  Formats schema names from the words the engine parsed them into.

  Engines with schema views at v1.0.0 and above write each schema name's
  words into the introspection JSON, under the top-level `__identifiers` key
  (see `hack/designs/identifier-casing.md`, "Schema JSON words"). Codegen
  formats those words itself instead of guessing word boundaries, so
  acronyms and plurals come out right (`prerequisiteSHAs` is
  `prerequisite_shas`, not `prerequisite_sh_as`).

  The words of the schema being generated are kept in the calling process
  (see `put_identifiers/1`), so the formatter can look them up by name. When
  a schema has no words (older engines), `words/1` returns `nil` and codegen
  keeps its own conversion.
  """

  @typedoc "A word of a schema name, as in the schema JSON."
  @type word :: %{
          kind: String.t(),
          text: String.t(),
          suffix: String.t(),
          capitalized: String.t()
        }

  @type casing :: :pascal | :camel | :snake | :screaming_snake | :kebab | :flat
  @type acronyms :: :uppercase | :capitalized

  @key {__MODULE__, :identifiers}

  @doc """
  Decode the `__identifiers` map of the schema JSON. Returns `nil` when it is
  absent.
  """
  @spec from_map(map() | nil) :: %{String.t() => [word()]} | nil
  def from_map(nil), do: nil

  def from_map(identifiers) when is_map(identifiers) do
    Map.new(identifiers, fn {name, words} -> {name, Enum.map(words, &word_from_map/1)} end)
  end

  @doc "Decode one word of the schema JSON."
  @spec word_from_map(map()) :: word()
  def word_from_map(word) do
    %{
      kind: Map.fetch!(word, "kind"),
      text: Map.fetch!(word, "text"),
      suffix: Map.get(word, "suffix", ""),
      capitalized: Map.fetch!(word, "capitalized")
    }
  end

  @doc """
  Use `identifiers` (from `from_map/1`, or `nil` for none) to format names in
  the calling process.
  """
  @spec put_identifiers(%{String.t() => [word()]} | nil) :: :ok
  def put_identifiers(identifiers) do
    Process.put(@key, identifiers)
    :ok
  end

  @doc """
  The words of `name` in the calling process's identifiers, or `nil` when
  there are none: the schema predates identifier words, or the name has no
  entry.
  """
  @spec words(String.t()) :: [word()] | nil
  def words(name) do
    case Process.get(@key) do
      %{^name => [_ | _] = words} -> words
      _ -> nil
    end
  end

  @doc """
  Format `words` in `casing`. `acronyms` selects how `:pascal` and `:camel`
  write acronyms and terms: `:uppercase` (`HTTPClient`) or `:capitalized`
  (`HttpClient`).
  """
  @spec format([word()], casing(), acronyms()) :: String.t()
  def format(words, casing, acronyms \\ :uppercase)

  def format(words, :snake, _), do: join_lower(words, "_")
  def format(words, :kebab, _), do: join_lower(words, "-")
  def format(words, :flat, _), do: join_lower(words, "")

  def format(words, :screaming_snake, _) do
    Enum.map_join(words, "_", &String.upcase(&1.text <> &1.suffix))
  end

  def format(words, :pascal, acronyms) do
    Enum.map_join(words, &capitalized_form(&1, acronyms))
  end

  def format([], :camel, _), do: ""

  def format([first | rest], :camel, acronyms) do
    String.downcase(first.text <> first.suffix) <> format(rest, :pascal, acronyms)
  end

  defp join_lower(words, sep) do
    Enum.map_join(words, sep, &String.downcase(&1.text <> &1.suffix))
  end

  defp capitalized_form(%{kind: "WORD"} = word, _), do: word.capitalized <> word.suffix
  defp capitalized_form(word, :capitalized), do: word.capitalized <> word.suffix
  defp capitalized_form(word, :uppercase), do: upcase_first(word.text) <> word.suffix

  defp upcase_first(<<first::utf8, rest::binary>>), do: String.upcase(<<first::utf8>>) <> rest
  defp upcase_first(""), do: ""
end
