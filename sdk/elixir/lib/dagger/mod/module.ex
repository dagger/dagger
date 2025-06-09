defmodule Dagger.Mod.Module do
  @moduledoc false

  alias Dagger.Mod.Helper
  alias Dagger.Mod.Object
  alias Dagger.Mod.Registry
  alias Dagger.Mod.Object.FieldDef
  alias Dagger.Mod.Object.FunctionDef

  @doc """
  Define a Dagger module from the given module.
  """
  @spec define(Dagger.Client.t(), module()) :: Dagger.Module.t()
  def define(dag, module) when is_struct(dag, Dagger.Client) and is_atom(module) do
    module
    |> Registry.register()
    |> Registry.all_modules()
    |> Enum.reduce(Dagger.Client.module(dag), &define(dag, &2, &1))
    |> maybe_with_description(Object.get_module_doc(module))
  end

  defp maybe_with_description(module, nil), do: module
  defp maybe_with_description(module, doc), do: Dagger.Module.with_description(module, doc)

  defp define(dag, dag_module, module) do
    case module.__kind__() do
      :object ->
        dag_module
        |> Dagger.Module.with_object(define_object(dag, module))

      :enum ->
        dag_module
        |> Dagger.Module.with_enum(define_enum(dag, module))
    end
  end

  defp define_object(dag, module) do
    mod_name = module.__object__(:name)

    dag
    |> Dagger.Client.type_def()
    |> Dagger.TypeDef.with_object(Helper.camelize(mod_name))
    |> then(&define_fields(&1, dag, module))
    |> then(&define_functions(&1, dag, module))
    |> then(&define_constructor(&1, dag, module))
  end

  defp define_enum(dag, module) do
    mod_name = module.__object__(:name)

    type_def =
      dag
      |> Dagger.Client.type_def()
      |> Dagger.TypeDef.with_enum(Helper.camelize(mod_name))

    Enum.reduce(module.__enum__(:keys), type_def, fn key, type_def ->
      value = module.__enum__(:value, key)
      doc = module.__enum__(:doc, key)
      opts = []

      opts =
        if doc do
          Keyword.put(opts, :description, doc)
        else
          opts
        end

      Dagger.TypeDef.with_enum_value(type_def, value, opts)
    end)
  end

  defp define_constructor(type_def, dag, module) do
    case Enum.find(module.__object__(:functions), &init?/1) do
      nil ->
        type_def

      {name, fun_def} ->
        type_def
        |> Dagger.TypeDef.with_constructor(
          FunctionDef.to_dag_function(fun_def, name, module, dag)
        )
    end
  end

  defp define_fields(type_def, dag, module) do
    module.__object__(:fields)
    |> Enum.reduce(type_def, fn {name, field_def}, type_def ->
      FieldDef.define(field_def, name, type_def, dag)
    end)
  end

  defp define_functions(type_def, dag, module) do
    module.__object__(:functions)
    |> Enum.reject(&init?/1)
    |> Enum.reduce(type_def, fn {name, fun_def}, type_def ->
      FunctionDef.define(fun_def, name, module, type_def, dag)
    end)
  end

  defp init?({:init, _}), do: true
  defp init?({_, _}), do: false
end
