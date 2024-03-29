defmodule Dagger.Codegen.ElixirGenerator.Formatter do
  alias Dagger.Codegen.Introspection.Types.TypeRef

  def format_module("Query"), do: format_module("Client")

  def format_module(name) do
    Module.concat(Dagger, Macro.camelize(name))
    |> to_string()
    |> String.trim_leading("Elixir.")
  end

  def format_var_name("Query"), do: format_var_name("Client")

  def format_var_name(name) do
    Macro.underscore(name)
  end

  # Temporarily fixes for issue https://github.com/dagger/dagger/issues/6310.
  @acronym_words %{
    "GPU" => "Gpu",
    "VCS" => "Vcs"
  }

  def format_function_name(name, acronym_words \\ @acronym_words) do
    acronym_words
    |> Enum.reduce(name, fn {word, new_word}, name ->
      String.replace(name, word, new_word)
    end)
    |> Macro.underscore()
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
          of_type: %TypeRef{kind: "SCALAR"}
        } = type
      ) do
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

  def format_output_type(%TypeRef{kind: "OBJECT", name: name}) do
    format_module(name)
  end

  def format_output_type(_type_ref), do: "DaggerInvalidOutput"
end
