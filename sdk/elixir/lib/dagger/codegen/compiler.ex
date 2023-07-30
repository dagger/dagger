defmodule Dagger.Codegen.Compiler do
  @moduledoc false

  # Compile GraphQL introspection into Elixir code.

  alias Dagger.Codegen.Elixir.Templates.Enum, as: EnumTmpl
  alias Dagger.Codegen.Elixir.Templates.Object
  alias Dagger.Codegen.Elixir.Templates.Scalar

  def compile(
        %{
          "__schema" => %{
            "types" => types
          }
        } = _introspection
      ) do
    # TODO: We should have a rewrite phase to rewrite type, annotate module information, etc. before
    # rendering type into module.
    types
    |> Enum.filter(fn type ->
      only_supported_kinds(type) and not_graphql_introspection_types(type)
    end)
    |> Enum.map(&render(&1, types))
  end

  defp only_supported_kinds(%{"kind" => kind}) do
    kind in ["ENUM", "OBJECT", "SCALAR"]
  end

  defp not_graphql_introspection_types(%{"name" => name}) do
    name not in graphql_introspection_types()
  end

  defp graphql_introspection_types() do
    [
      "__Type",
      "__Directive",
      "__Field",
      "__InputValue",
      "__EnumValue",
      "__Schema",
      "__TypeKind",
      "__DirectiveLocation"
    ]
  end

  defp render(%{"name" => name, "kind" => kind} = full_type, types) do
    q =
      case kind do
        "OBJECT" ->
          Object.render(full_type, types)

        "SCALAR" ->
          Scalar.render(full_type)

        "ENUM" ->
          EnumTmpl.render(full_type)
      end

    name = if(name == "Query", do: "Client", else: name)

    {"#{Macro.underscore(name)}.ex", q}
  end
end
