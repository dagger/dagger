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
  alias Dagger.Codegen.Elixir.Templates.ObjectTmpl

  def compile(introspection) do
    compile_types(introspection["__schema"]["types"])
  end

  defp compile_types(types) do
    compile_modules(types |> Enum.filter(&(&1["kind"] == "OBJECT")))
  end

  defp compile_modules(object_types) do
    object_types
    |> Enum.filter(&(&1["name"] not in graphql_introspection_types()))
    |> Enum.map(&render_object/1)
  end

  defp graphql_introspection_types() do
    ["__Type", "__Directive", "__Field", "__InputValue", "__EnumValue", "__Schema"]
  end

  defp render_object(%{"name" => name} = full_type) do
    {"#{Macro.underscore(name)}.ex", ObjectTmpl.render_object(full_type)}
  end
end
