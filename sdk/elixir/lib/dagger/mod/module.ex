defmodule Dagger.Mod.Module do
  @moduledoc false

  alias Dagger.Mod.Function
  alias Dagger.Mod.Object
  alias Dagger.Mod.Helper

  @doc """
  Define a Dagger module from the given module.
  """
  @spec define(Dagger.Client.t(), module()) :: Dagger.Module.t()
  def define(dag, module) when is_struct(dag, Dagger.Client) and is_atom(module) do
    dag
    |> Dagger.Client.module()
    |> Dagger.Module.with_object(define_object(dag, module))
    |> maybe_with_description(Object.get_module_doc(module))
  end

  defp maybe_with_description(module, nil), do: module
  defp maybe_with_description(module, doc), do: Dagger.Module.with_description(module, doc)

  defp define_object(dag, module) do
    mod_name = module.__object__(:name)
    functions = module.__object__(:functions)

    type_def =
      dag
      |> Dagger.Client.type_def()
      |> Dagger.TypeDef.with_object(Helper.camelize(mod_name))

    functions
    |> Enum.map(&Function.define(dag, module, &1))
    |> Enum.reduce(
      type_def,
      &Dagger.TypeDef.with_function(&2, &1)
    )
  end
end
