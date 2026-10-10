defmodule Dagger.Codegen.Naming do
  @moduledoc """
  Looks up schema names as the engine formatted them.

  Codegen doesn't format names itself: the engine does, with
  `Query.formatIdentifiers`, so the formatting rules and the naming
  dictionary live only in the engine (see `hack/designs/identifier-casing.md`,
  "Formatting names in codegen"). Codegen has no engine session, so it reads
  the names from a file the online step writes next to the introspection
  JSON (`codegen introspect --names-out`), which maps each name format to the
  schema's names formatted in it:

      %{"SNAKE:UPPERCASE" => %{"prerequisiteSHAs" => "prerequisite_shas"}}

  The names of the schema being generated are kept in the calling process
  (see `put_names/1`), so the formatter can look them up. For a format or
  name the file doesn't have, or a schema without `Query.formatIdentifiers`
  (older engines), `format/3` returns `nil` and codegen keeps its legacy
  conversion.
  """

  @type casing :: :pascal | :camel | :snake | :screaming_snake | :kebab | :flat
  @type acronyms :: :uppercase | :capitalized
  @typedoc "Formatted names by name format (`\"SNAKE:UPPERCASE\"`), then by schema name."
  @type names :: %{String.t() => %{String.t() => String.t()}}

  # The name formats the Elixir generator uses: modules in PascalCase,
  # everything else in snake_case.
  @formats [{:pascal, :uppercase}, {:snake, :uppercase}]

  @key {__MODULE__, :names}

  @doc """
  The name formats the Elixir generator uses, as `CASING:ACRONYMS` keys of
  the names file.
  """
  @spec formats() :: [String.t()]
  def formats, do: Enum.map(@formats, fn {casing, acronyms} -> key(casing, acronyms) end)

  @doc "The names file key of a format: `key(:snake, :uppercase)` is `\"SNAKE:UPPERCASE\"`."
  @spec key(casing(), acronyms()) :: String.t()
  def key(casing, acronyms), do: String.upcase("#{casing}:#{acronyms}")

  @doc """
  Decode the names file. Returns `nil` for `nil`; formats that aren't maps,
  and names that aren't non-empty strings, are left out.
  """
  @spec from_map(map() | nil) :: names() | nil
  def from_map(nil), do: nil

  def from_map(names) when is_map(names) do
    for {format, formatted} when is_map(formatted) <- names, into: %{} do
      {format, Map.filter(formatted, fn {_name, value} -> is_binary(value) and value != "" end)}
    end
  end

  @doc """
  Use `names` (from `from_map/1`, or `nil` for none) to format names in the
  calling process.
  """
  @spec put_names(names() | nil) :: :ok
  def put_names(names) do
    Process.put(@key, names)
    :ok
  end

  @doc """
  `name` as the engine formatted it in `casing` and `acronyms`, from the
  calling process's names, or `nil` when it has none: the schema predates
  `Query.formatIdentifiers`, or the format or name has no entry.
  """
  @spec format(String.t(), casing(), acronyms()) :: String.t() | nil
  def format(name, casing, acronyms \\ :uppercase) do
    key = key(casing, acronyms)

    case Process.get(@key) do
      %{^key => %{^name => formatted}} -> formatted
      _ -> nil
    end
  end
end
