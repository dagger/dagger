defmodule Dagger.Codegen.ElixirGenerator.Renderer do
  @moduledoc """
  Provides functions to render small part of Elixir code.
  """

  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.Introspection.Types.Type
  alias Dagger.Codegen.Introspection.Types.Field

  @doc """
  Render the string.

  Uses multiline string when newline is detected.
  """
  def render_string(s) do
    if String.contains?(s, "\n") do
      [
        "\"\"\"",
        ?\n,
        s,
        ?\n,
        "\"\"\""
      ]
    else
      [?", s, ?"]
    end
  end

  @doc "Render atom value."
  def render_atom(string), do: ":#{string}"

  @doc """
  Render moduledoc from `type`.

  Uses the module name when no description in the type.
  """
  def render_moduledoc(type)

  def render_moduledoc(%Type{description: desc, name: name} = type)
      when desc == "" or is_nil(desc) do
    # Prevent ExDoc ignore documentation when moduledoc were not found.
    render_moduledoc(%{type | description: Formatter.format_module(name)})
  end

  def render_moduledoc(%Type{} = type) do
    [
      "@moduledoc",
      ~c" ",
      render_string(type.description)
    ]
    |> IO.iodata_to_binary()
  end

  @doc """
  Render function document.
  """
  def render_doc(field_or_enum)

  def render_doc(%{description: ""}), do: ""

  def render_doc(%{description: description}) do
    ["@doc", ~c" ", render_string(description)]
    |> IO.iodata_to_binary()
  end

  @doc """
  Render function deprecation message.
  """
  def render_deprecated(field)

  def render_deprecated(%Field{deprecation_reason: ""}), do: ""
  def render_deprecated(%Field{deprecation_reason: nil}), do: ""

  def render_deprecated(%Field{deprecation_reason: reason}) do
    ["@deprecated", ~c" ", render_string(Formatter.format_doc(reason))]
    |> IO.iodata_to_binary()
  end

  @doc "Render possible values in typespec."
  def render_union_type(types), do: Enum.join(types, " | ")
end
