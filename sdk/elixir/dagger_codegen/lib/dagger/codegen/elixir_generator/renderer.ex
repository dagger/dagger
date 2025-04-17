defmodule Dagger.Codegen.ElixirGenerator.Renderer do
  @moduledoc """
  Provides functions to render small part of Elixir code.
  """

  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.Introspection.Types.Type
  alias Dagger.Codegen.Introspection.Types.Field

  @doc """
  Render the module.
  """
  def render_module(type, body) do
    module = Formatter.format_module(type.name)

    [
      "# This file generated by `dagger_codegen`. Please DO NOT EDIT.",
      ?\n,
      "defmodule #{module} do",
      ?\n,
      render_moduledoc(type),
      ?\n,
      ?\n,
      body,
      ?\n,
      "end"
    ]
  end

  @doc """
  Render the string.

  Uses multiline string when newline is detected.
  """
  def render_string(s) do
    s = escape(s)

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

  defp escape(s), do: s |> String.replace("\\", "\\\\") |> String.replace("\"", "\\\"")

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
  end

  @doc """
  Render function document.
  """
  def render_doc(field_or_enum)

  def render_doc(%{description: ""}), do: ""

  def render_doc(%{description: description}) do
    ["@doc", ~c" ", render_string(description)]
  end

  @doc """
  Render function deprecation message.
  """
  def render_deprecated(field)

  def render_deprecated(%Field{deprecation_reason: ""}), do: ""
  def render_deprecated(%Field{deprecation_reason: nil}), do: ""

  def render_deprecated(%Field{deprecation_reason: reason}) do
    ["@deprecated", ~c" ", render_string(Formatter.format_doc(reason))]
  end
end
