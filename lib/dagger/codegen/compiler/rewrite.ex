defmodule Dagger.Codegen.Compiler.Rewrite do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Module, as: Mod

  # Rewrite graphql introspection type to make it easier to
  # compile.

  def rewrite(type) do
    type
    |> make_private()
    |> gen_module_name()
  end

  defp make_private(type) do
    Map.put(type, "private", %{})
  end

  defp gen_module_name(%{"name" => name} = type) do
    type
    |> put_private("mod_name", Module.concat([Dagger, Mod.format_name(name)]))
  end

  defp put_private(%{"private" => private} = type, key, value) do
    %{type | "private" => Map.put(private, key, value)}
  end
end
