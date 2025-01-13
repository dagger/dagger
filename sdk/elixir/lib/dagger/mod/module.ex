defmodule Dagger.Mod.Module do
  @moduledoc false

  alias Dagger.Mod.Helper
  alias Dagger.Mod.Object
  alias Dagger.Mod.Object.FieldDef
  alias Dagger.Mod.Object.FunctionDef

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

    dag
    |> Dagger.Client.type_def()
    |> Dagger.TypeDef.with_object(Helper.camelize(mod_name))
    |> then(&define_fields(&1, dag, module))
    |> then(&define_functions(&1, dag, module))
  end

  defp define_fields(type_def, dag, module) do
    module.__object__(:fields)
    |> Enum.reduce(type_def, fn {name, field_def}, type_def ->
      FieldDef.define(field_def, name, type_def, dag)
    end)
  end

  defp define_functions(type_def, dag, module) do
    module.__object__(:functions)
    |> Enum.reduce(type_def, fn {name, fun_def}, type_def ->
      FunctionDef.define(fun_def, name, module, type_def, dag)
    end)
  end
end
