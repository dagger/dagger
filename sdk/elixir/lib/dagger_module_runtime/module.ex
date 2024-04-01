defmodule Dagger.ModuleRuntime.Module do
  @moduledoc false

  alias Dagger.ModuleRuntime.Function
  alias Dagger.ModuleRuntime.Helper

  @doc """
  Define a Dagger module.
  """
  def define(dag, module) when is_struct(dag, Dagger.Client) and is_atom(module) do
    dag
    |> Dagger.Client.module()
    |> Dagger.Module.with_object(define_object(dag, module))
  end

  @doc """
  Get the name of the given `module`.
  """
  def name_for(module) do
    module.__info__(:attributes)
    |> Keyword.fetch!(:name)
    |> to_string()
  end

  @doc """
  Get the function definitions of the given `module`.
  """
  def functions_for(module) do
    module.__info__(:attributes)
    |> Keyword.fetch!(:functions)
  end

  def get_function_definition(module, name) do
    functions_for(module)
    |> Keyword.fetch!(name)
  end

  defp define_object(dag, module) do
    mod_name = name_for(module)
    functions = functions_for(module)

    type_def =
      dag
      |> Dagger.Client.type_def()
      |> Dagger.TypeDef.with_object(Helper.camelize(mod_name))

    functions
    |> Enum.map(&Function.define(dag, &1))
    |> Enum.reduce(
      type_def,
      &Dagger.TypeDef.with_function(&2, &1)
    )
  end
end
