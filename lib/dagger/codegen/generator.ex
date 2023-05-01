defmodule Dagger.Codegen.Generator do
  @moduledoc false

  def generate() do
    {:ok, client} = Dagger.Client.connect()

    {:ok, %{status: 200, body: resp}} =
      Dagger.Client.query(client, Dagger.Codegen.Introspection.query())

    Dagger.Codegen.Compiler.compile(resp["data"])
  end
end

defmodule Dagger.Codegen.Compiler do
  @moduledoc false

  # Compile GraphQL introspection into Elixir code.

  alias Dagger.Codegen.Elixir.Templates.EnumTmpl
  alias Dagger.Codegen.Elixir.Templates.ObjectTmpl
  alias Dagger.Codegen.Elixir.Templates.ScalarTmpl

  def compile(
        %{
          "__schema" => %{
            "types" => types
          }
        } = _introspection
      ) do
    compile_types(types)
  end

  defp compile_types(types) do
    compile_modules(
      types |> Enum.filter(&(&1["kind"] in ["ENUM", "OBJECT", "SCALAR"])),
      graphql_introspection_types()
    )
  end

  defp compile_modules(object_types, excludes) when is_list(excludes) do
    object_types
    |> Enum.filter(&(&1["name"] not in excludes))
    |> Enum.map(&render/1)
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
      "__DirectiveLocation",
      "Int",
      "Float",
      "String",
      "ID",
      "Boolean",
      "DateTime"
    ]
  end

  defp render(%{"name" => name, "kind" => "OBJECT"} = full_type) do
    {"#{Macro.underscore(name)}.ex", ObjectTmpl.render_object(full_type)}
  end

  defp render(%{"name" => name, "kind" => "SCALAR"} = full_type) do
    {"#{Macro.underscore(name)}.ex", ScalarTmpl.render_scalar(full_type)}
  end

  defp render(%{"name" => name, "kind" => "ENUM"} = full_type) do
    {"#{Macro.underscore(name)}.ex", EnumTmpl.render_enum(full_type)}
  end
end
