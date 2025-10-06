defmodule Dagger.Mod.Registry do
  @moduledoc false

  defstruct modules: %{}

  def register(root_module) do
    root_module
    |> traverse()
    |> Enum.reduce(%__MODULE__{}, &put_module(&2, &1))
  end

  def put_module(%__MODULE__{} = registry, module) when is_atom(module) do
    %__MODULE__{modules: Map.put(registry.modules, module.__name__(), module)}
  end

  def all_modules(%__MODULE__{} = registry) do
    Map.values(registry.modules) |> Enum.sort()
  end

  def get_module_by_name!(%__MODULE__{} = registry, name) when is_binary(name) do
    case registry.modules[name] do
      nil -> raise "Cannot find `#{name}` in the registry."
      module -> module
    end
  end

  defp traverse(root_module) do
    root_module.__object__(:functions)
    |> traverse([root_module])
  end

  defp traverse([], modules), do: modules |> List.flatten() |> Enum.uniq() |> Enum.reverse()

  defp traverse([{_, fun_def} | funs], modules) do
    module = fun_def.return

    if object?(module) and module not in modules do
      traverse(funs, [traverse(module) | modules])
    else
      traverse(funs, modules)
    end
  end

  defp object?(:integer), do: false
  defp object?(:float), do: false
  defp object?(:boolean), do: false
  defp object?(:string), do: false
  defp object?({:list, type}), do: object?(type)
  defp object?({:optional, type}), do: object?(type)

  defp object?(type),
    do: type.__kind__() == :object and function_exported?(type, :__object__, 1)
end
