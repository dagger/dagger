defmodule Dagger.Mod do
  @moduledoc false

  alias Dagger.Mod.Encoder
  alias Dagger.Mod.Decoder

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

    with {:ok, parent_name} <- Dagger.FunctionCall.parent_name(fn_call),
         {:ok, fn_name} <- Dagger.FunctionCall.name(fn_call),
         {:ok, parent_json} <- Dagger.FunctionCall.parent(fn_call),
         {:ok, parent} <- Jason.decode(parent_json),
         {:ok, input_args} <- Dagger.FunctionCall.input_args(fn_call),
         input_args = fetch_args!(input_args),
         {:ok, json} <- invoke(dag, module, parent, parent_name, fn_name, input_args) do
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
  def invoke(dag, module, _parent, "", _fn_name, _input_args) do
    dag
    |> Dagger.Mod.Module.define(module)
    |> Encoder.validate_and_encode(Dagger.Module)
  end

  # Invoke function.
  def invoke(dag, module, _parent, _parent_name, fn_name, input_args) do
    fun_name = fn_name |> Macro.underscore() |> String.to_existing_atom()
    fun_def = module.__object__(:function, fun_name)
    args = decode_args!(dag, input_args, fun_def[:args])
    return_type = fun_def[:return]

    case apply(module, fun_name, args) do
      {:error, _} = error -> error
      {:ok, result} -> Encoder.validate_and_encode(result, return_type)
      result -> Encoder.validate_and_encode(result, return_type)
    end
  end

  defp fetch_args!(input_args) do
    Enum.into(input_args, %{}, fn arg ->
      {:ok, name} = Dagger.FunctionCallArgValue.name(arg)
      {:ok, value} = Dagger.FunctionCallArgValue.value(arg)
      {Macro.underscore(name), value}
    end)
  end

  # Get the value from a given `input_args` and make it positional by `args_def`.
  def decode_args!(dag, input_args, arg_defs) do
    for {name, arg_def} <- arg_defs do
      {:ok, value} =
        input_args
        |> Map.get(to_string(name))
        |> Decoder.decode(arg_def[:type], dag)

      value
    end
  end

  defp format_error(%{__exception__: true} = exception), do: Exception.message(exception)
  defp format_error(error) when is_binary(error) or is_atom(error), do: error
  defp format_error(error), do: inspect(error)
end
