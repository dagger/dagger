defmodule Dagger.Mod.Module do
  @moduledoc false

  alias Dagger.Mod.Function
  alias Dagger.Mod.Helper

  @doc """
  Define a Dagger module.
  """
  def define(dag, module) when is_struct(dag, Dagger.Client) and is_atom(module) do
    {:docs_v1, _, :elixir, _, module_doc, _, function_docs} = Code.fetch_docs(module)

    dag
    |> Dagger.Client.module()
    |> Dagger.Module.with_object(define_object(dag, module, function_docs))
    |> maybe_with_description(module_doc)
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

  defp maybe_with_description(module, doc) when doc in [:none, :hidden], do: module

  defp maybe_with_description(module, %{"en" => doc}) do
    Dagger.Module.with_description(module, doc)
  end

  defp define_object(dag, module, function_docs) do
    mod_name = name_for(module)
    functions = functions_for(module)

    type_def =
      dag
      |> Dagger.Client.type_def()
      |> Dagger.TypeDef.with_object(Helper.camelize(mod_name))

    functions
    |> Enum.map(&Function.define(dag, &1, find_doc_content(function_docs, &1)))
    |> Enum.reduce(
      type_def,
      &Dagger.TypeDef.with_function(&2, &1)
    )
  end

  defp find_doc_content(function_docs, {name, _}) do
    fun = fn
      {{:function, ^name, 2}, _, _, _, _} -> true
      _ -> false
    end

    case Enum.find(function_docs, fun) do
      nil -> :none
      {{:function, ^name, 2}, _, _, doc_content, _} -> doc_content
    end
  end
end
