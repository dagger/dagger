defmodule Dagger.Mod.Registry do
  @moduledoc false

  defstruct modules: %{}, enums: []

  def register(root_module) do
    root_module
    |> traverse()
    |> Enum.reduce(%__MODULE__{}, &put_module(&2, &1))
  end

  def put_module(%__MODULE__{} = registry, module) when is_atom(module) do
    if function_exported?(module, :__kind__, 0) do
      case module.__kind__() do
        :enum ->
          %__MODULE__{registry | enums: [module | registry.enums]}

        :object ->
          %__MODULE__{registry | modules: Map.put(registry.modules, module.__name__(), module)}

        _ ->
          registry
      end
    else
      registry
    end
  end

  def all_modules(%__MODULE__{} = registry) do
    (Map.values(registry.modules) ++ registry.enums) |> Enum.sort()
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
    enum_modules = collect_enums_from_funs(fun_def)

    ret_module = fun_def.return

    additional_modules =
      if object?(ret_module) and ret_module not in modules do
        traverse(ret_module)
      else
        []
      end

    modules_to_add = enum_modules ++ additional_modules

    traverse(funs, [modules_to_add | modules])
  end

  defp collect_enums_from_funs(fun_def) do
    fun_def.args
    |> Enum.flat_map(fn {_name, arg_def} ->
      type = Keyword.fetch!(arg_def, :type)
      collect_enums_from_args(type)
    end)
  end

  defp collect_enums_from_args({:optional, type}), do: collect_enums_from_args(type)
  defp collect_enums_from_args({:list, type}), do: collect_enums_from_args(type)

  defp collect_enums_from_args(module) when is_atom(module) do
    if Code.ensure_loaded?(module) and function_exported?(module, :__kind__, 0) and
         module.__kind__() == :enum do
      [module]
    else
      []
    end
  end

  defp collect_enums_from_args(_), do: []

  defp object?(:integer), do: false
  defp object?(:float), do: false
  defp object?(:boolean), do: false
  defp object?(:string), do: false
  defp object?({:list, type}), do: object?(type)
  defp object?({:optional, type}), do: object?(type)

  defp object?(type),
    do: type.__kind__() == :object and function_exported?(type, :__object__, 1)
end
