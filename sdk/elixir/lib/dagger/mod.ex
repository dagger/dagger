defmodule Dagger.Mod do
  @moduledoc false

  alias Dagger.Mod.Encoder
  alias Dagger.Mod.Scope
  alias Dagger.Mod.Registry

  @doc """
  Invoke a function.
  """
  def invoke(module) when is_atom(module) do
    case Dagger.Global.start_link() do
      {:ok, _} -> invoke(Dagger.Global.dag(), module)
      otherwise -> otherwise
    end
  end

  def invoke(dag, module) do
    fn_call = Dagger.Client.current_function_call(dag)

    with {:ok, scope} <- Scope.from_fn_call(fn_call),
         {:ok, json} <- invoke(dag, module, scope) do
      Dagger.FunctionCall.return_value(fn_call, json)
    else
      {:error, error} ->
        IO.puts(:stderr, format_error(error))
        exit({:shutdown, 2})
    end
  after
    Dagger.Global.close()
  end

  # Register module.
  def invoke(dag, module, %Scope{parent_name: ""}) do
    dag
    |> Dagger.Mod.Module.define(module)
    |> Encoder.validate_and_encode(Dagger.Module)
  end

  # Constructor call.
  def invoke(dag, module, %Scope{fn_name: ""} = scope) do
    fun_def = module.__object__(:function, :init)
    args = Scope.args!(scope, fun_def.args, dag)
    return_type = fun_def.return

    execute_function(module, :init, args, return_type)
  end

  # Invoke function.
  def invoke(dag, root_module, %Scope{} = scope) do
    registry = Registry.register(root_module)
    module = Registry.get_module_by_name!(registry, scope.parent_name)
    {:ok, parent} = Scope.into_module(scope, module, dag)
    fun_name = Scope.fn_name(scope)
    fun_def = module.__object__(:function, fun_name)

    args =
      scope
      |> Scope.args!(fun_def.args, dag)
      |> then(fn args ->
        if(fun_def.self, do: [parent | args], else: args)
      end)

    return_type = fun_def.return

    execute_function(module, fun_name, args, return_type)
  end

  defp execute_function(module, fun_name, args, return_type) do
    case apply(module, fun_name, args) do
      {:error, _} = error -> error
      {:ok, result} -> Encoder.validate_and_encode(result, return_type)
      result -> Encoder.validate_and_encode(result, return_type)
    end
  end

  defp format_error(%{__exception__: true} = exception), do: Exception.message(exception)
  defp format_error(error) when is_binary(error) or is_atom(error), do: error
  defp format_error(error), do: inspect(error)
end
