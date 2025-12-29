defmodule Dagger.Mod.Object.FunctionDef do
  @moduledoc false

  # A function declaration from `Dagger.Mod.Object.defn/2`.

  @enforce_keys [:self, :args, :return]
  defstruct @enforce_keys ++ [:cache_policy]

  @doc """
  Convert a `fun_def` into Dagger Function.
  """
  def to_dag_function(%__MODULE__{} = fun_def, name, module, %Dagger.Client{} = dag)
      when is_atom(name) and is_atom(module) do
    dag
    |> Dagger.Client.function(
      Dagger.Mod.Helper.camelize(name),
      Dagger.Mod.Object.TypeDef.define(dag, fun_def.return)
    )
    |> maybe_with_description(Dagger.Mod.Object.get_function_doc(module, name))
    |> maybe_with_cache_policy(Dagger.Mod.Object.get_function_cache_policy(fun_def))
    |> maybe_with_deprecated(Dagger.Mod.Object.get_function_deprecated(module, name))
    |> with_args(fun_def.args, dag)
  end

  @doc """
  Define a Dagger Function from `fun_def`.
  """
  def define(
        %__MODULE__{} = fun_def,
        name,
        module,
        %Dagger.TypeDef{} = type_def,
        %Dagger.Client{} = dag
      )
      when is_atom(name) and is_atom(module) do
    Dagger.TypeDef.with_function(type_def, to_dag_function(fun_def, name, module, dag))
  end

  defp maybe_with_deprecated(function, nil), do: function

  defp maybe_with_deprecated(function, {:deprecated, reason}),
    do: Dagger.Function.with_deprecated(function, reason: reason)

  defp maybe_with_cache_policy(function, nil), do: function

  defp maybe_with_cache_policy(function, {policy, ttl}),
    do: Dagger.Function.with_cache_policy(function, policy, ttl)

  defp maybe_with_cache_policy(function, policy),
    do: Dagger.Function.with_cache_policy(function, policy)

  defp maybe_with_description(function, nil), do: function
  defp maybe_with_description(function, doc), do: Dagger.Function.with_description(function, doc)

  defp with_args(fun, args, dag) do
    args
    |> Enum.reduce(fun, fn {name, arg_def}, fun ->
      type = Keyword.fetch!(arg_def, :type)

      type_def =
        dag
        |> Dagger.Mod.Object.TypeDef.define(type)

      opts =
        arg_def
        |> Keyword.take([:doc, :default, :default_path, :ignore, :deprecated])
        |> Enum.reject(fn {_, value} -> is_nil(value) end)
        |> Enum.map(&normalize_arg_option/1)

      fun
      |> Dagger.Function.with_arg(name, type_def, opts)
    end)
  end

  defp normalize_arg_option({:doc, doc}), do: {:description, doc}

  defp normalize_arg_option({:default, default_value}),
    do: {:default_value, Jason.encode!(default_value)}

  defp normalize_arg_option({:deprecation, reason}), do: {:deprecation, reason}

  defp normalize_arg_option(opt), do: opt
end
