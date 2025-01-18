defmodule Dagger.Mod do
  @moduledoc false

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

  def invoke(dag, module, _parent, "", _fn_name, _input_args) do
    dag
    |> Dagger.Mod.Module.define(module)
    |> encode(Dagger.Module)
  end

  def invoke(dag, module, _parent, _parent_name, fn_name, input_args) do
    fun = fn_name |> Macro.underscore() |> String.to_existing_atom()
    fun_def = module.__object__(:function, fun)
    args = decode_args(dag, input_args, Keyword.fetch!(fun_def, :args))
    return_type = Keyword.fetch!(fun_def, :return)

    case apply(module, fun, args) do
      {:error, _} = error -> error
      {:ok, result} -> encode(result, return_type)
      result -> encode(result, return_type)
    end
  end

  def decode_args(dag, input_args, args_def) do
    args =
      Enum.into(input_args, %{}, fn arg ->
        {:ok, name} = Dagger.FunctionCallArgValue.name(arg)
        {:ok, value} = Dagger.FunctionCallArgValue.value(arg)
        name = String.to_existing_atom(name)
        {:ok, value} = decode(value, get_in(args_def, [name, :type]), dag)
        {name, value}
      end)

    for {name, _} <- args_def do
      Map.get(args, name)
    end
  end

  def decode(value, type, dag) do
    with {:ok, value} <- Jason.decode(value) do
      cast(value, type, dag)
    end
  end

  defp cast(value, :integer, _) when is_integer(value) do
    {:ok, value}
  end

  defp cast(value, :boolean, _) when is_boolean(value) do
    {:ok, value}
  end

  defp cast(value, :string, _) when is_binary(value) do
    {:ok, value}
  end

  defp cast(values, {:list, type}, dag) when is_list(values) do
    values =
      for value <- values do
        {:ok, value} = cast(value, type, dag)
        value
      end

    {:ok, values}
  end

  defp cast(nil, {:optional, _type}, _dag), do: {:ok, nil}
  defp cast(value, {:optional, type}, dag), do: cast(value, type, dag)

  defp cast(value, module, dag) when is_binary(value) and is_atom(module) do
    # NOTE: It feels like we really need a protocol for the module to 
    # load the data from id.
    ["Dagger", name] = Module.split(module)
    name = Macro.underscore(name)
    fun = String.to_existing_atom("load_#{name}_from_id")
    {:ok, apply(Dagger.Client, fun, [dag, value])}
  end

  defp cast(value, type, _) do
    {:error, "cannot cast value #{value} to type #{type}"}
  end

  def encode(result, type) do
    with {:ok, value} <- dump(result, type) do
      Jason.encode(value)
    end
  end

  defp dump(value, :integer) when is_integer(value) do
    {:ok, value}
  end

  defp dump(value, :boolean) when is_boolean(value) do
    {:ok, value}
  end

  defp dump(value, :string) when is_binary(value) do
    {:ok, value}
  end

  defp dump(values, {:list, type}) when is_list(values) do
    values =
      for value <- values do
        {:ok, value} = dump(value, type)
        value
      end

    {:ok, values}
  end

  defp dump(_, Dagger.Void) do
    {:ok, nil}
  end

  defp dump(%module{} = struct, module) do
    value =
      if function_exported?(module, :id, 1) do
        Dagger.ID.id!(struct)
      else
        struct
      end

    {:ok, value}
  end

  defp dump(value, type) do
    {:error, "cannot dump value #{value} to type #{type}"}
  end

  defp format_error(%{__exception__: true} = exception), do: Exception.message(exception)
  defp format_error(error) when is_binary(error) or is_atom(error), do: error
  defp format_error(error), do: inspect(error)
end
