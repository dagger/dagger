defmodule Dagger.Codegen.ElixirGenerator.Formatter do
  @moduledoc """
  Formats schema names into Elixir names.

  Names are the engine's when it formatted them (see `Dagger.Codegen.Naming`):
  modules in PascalCase with uppercase acronyms (`Dagger.JSONValue`),
  functions, arguments and variables in snake_case. Older schemas have no
  formatted names, so names fall back to the legacy `Macro` conversion, which
  keeps their output unchanged.

  Only Elixir identifiers are formatted here: the names sent to the API
  (selected fields, arguments, enum values, type names) are always the
  schema's own.
  """

  alias Dagger.Codegen.Introspection.Types.TypeRef
  alias Dagger.Codegen.Naming

  def format_module("Query"), do: format_module("Client")

  def format_module(name) do
    case Naming.format(name, :pascal, :uppercase) do
      nil -> legacy_module(name)
      formatted -> "Dagger." <> formatted
    end
  end

  defp legacy_module(name) do
    Module.concat(Dagger, Macro.camelize(name))
    |> to_string()
    |> String.trim_leading("Elixir.")
  end

  def format_var_name("Query"), do: format_var_name("Client")

  def format_var_name(name) do
    case Naming.format(name, :snake, :uppercase) do
      nil -> legacy_var_name(name)
      formatted -> formatted
    end
  end

  @doc """
  The legacy conversion of `format_var_name/1`, for names that must not
  change: the input object struct keys, which are sent to the API as is.
  """
  def legacy_var_name("Query"), do: legacy_var_name("Client")
  def legacy_var_name(name), do: Macro.underscore(name)

  def format_function_name(name) do
    case Naming.format(name, :snake, :uppercase) do
      nil -> legacy_function_name(name)
      formatted -> formatted |> normalize_reserved_word() |> question_mark()
    end
  end

  @doc """
  The function name `format_function_name/1` gives `name` when the engine
  didn't format the schema's names, to keep renamed functions as deprecated
  aliases.
  """
  def legacy_function_name(name) do
    name
    |> normalize_name()
    |> Macro.underscore()
    |> question_mark()
  end

  # Special case: is_foo => foo?
  defp question_mark(<<"is_", rest::binary>>), do: rest <> "?"
  defp question_mark(name), do: name

  defp normalize_name(name) do
    name
    |> normalize_acronym_word()
    |> normalize_reserved_word()
  end

  @reserved_words [
    # From https://hexdocs.pm/elixir/1.16.2/syntax-reference.html
    "true",
    "false",
    "nil",
    "when",
    "and",
    "or",
    "not",
    "in",
    "fn",
    "do",
    "end",
    "catch",
    "rescue",
    "after",
    "else"
  ]

  defp normalize_reserved_word(name, reserved_words \\ @reserved_words) do
    if name in reserved_words do
      "#{name}_"
    else
      name
    end
  end

  # Temporarily fixes for issue https://github.com/dagger/dagger/issues/6310.
  @acronym_words %{
    "GPU" => "Gpu",
    "VCS" => "Vcs"
  }

  defp normalize_acronym_word(name, acronym_words \\ @acronym_words) do
    acronym_words
    |> Enum.reduce(name, fn {word, new_word}, name ->
      String.replace(name, word, new_word)
    end)
  end

  def format_doc(doc) do
    doc = String.replace(doc, "\"", "\\\"")

    for [text, api] <- Regex.scan(~r/`(?<name>[a-zA-Z0-9]+)`/, doc),
        reduce: doc do
      reason -> String.replace(reason, text, "`#{format_function_name(api)}`")
    end
  end

  def format_type(%TypeRef{
        kind: "LIST",
        of_type: %TypeRef{kind: "NON_NULL", of_type: type}
      }) do
    "[#{format_type(type, false)}]"
  end

  def format_type(%TypeRef{kind: "NON_NULL", of_type: type}) do
    if type.kind == "LIST" do
      format_type(type)
    else
      format_type(type, false)
    end
  end

  def format_type(%TypeRef{} = type) do
    format_type(type, true)
  end

  defp format_type(%TypeRef{kind: "SCALAR", name: name}, nullable?) do
    type =
      case name do
        "String" -> "String.t()"
        "Int" -> "integer()"
        "Float" -> "float()"
        "Boolean" -> "boolean()"
        "DateTime" -> "DateTime.t()"
        "ID" -> "String.t()"
        otherwise -> "#{format_module(otherwise)}.t()"
      end

    if nullable? do
      "#{type} | nil"
    else
      type
    end
  end

  # OBJECT, INPUT_OBJECT, ENUM
  defp format_type(%TypeRef{name: name}, nullable?) do
    type = "#{format_module(name)}.t()"

    if nullable? do
      "#{type} | nil"
    else
      type
    end
  end

  def format_typespec_output_type(
        %TypeRef{
          kind: "NON_NULL",
          of_type: %TypeRef{kind: kind}
        } = type
      )
      when kind in ["SCALAR", "ENUM"] do
    "{:ok, #{format_type(type)}} | {:error, term()}"
  end

  def format_typespec_output_type(
        %TypeRef{
          kind: "SCALAR"
        } = type
      ) do
    "{:ok, #{format_type(type)}} | {:error, term()}"
  end

  def format_typespec_output_type(%TypeRef{
        kind: "NON_NULL",
        of_type: %TypeRef{kind: "LIST"} = type
      }) do
    "{:ok, #{format_type(type)}} | {:error, term()}"
  end

  def format_typespec_output_type(
        %TypeRef{
          kind: "LIST"
        } = type
      ) do
    "{:ok, #{format_type(type)}} | {:error, term()}"
  end

  def format_typespec_output_type(%TypeRef{kind: kind} = type)
      when kind in ["OBJECT", "INTERFACE"] do
    "{:ok, #{format_type(type)}} | {:error, term()}"
  end

  def format_typespec_output_type(type) do
    format_type(type)
  end

  # TODO: clarify which pattern match use.
  def format_output_type(%TypeRef{
        kind: "NON_NULL",
        of_type: %TypeRef{kind: "LIST", of_type: type}
      }) do
    format_output_type(type)
  end

  def format_output_type(%TypeRef{
        kind: "NON_NULL",
        of_type: type
      }) do
    format_output_type(type)
  end

  def format_output_type(%TypeRef{
        kind: "LIST",
        of_type: type
      }) do
    format_output_type(type)
  end

  def format_output_type(%TypeRef{kind: kind, name: name}) when kind in ["OBJECT", "INTERFACE", "ENUM"] do
    format_module(name)
  end

  def format_output_type(type_ref) do
    raise "Cannot format output type for #{inspect(type_ref)}"
  end
end
