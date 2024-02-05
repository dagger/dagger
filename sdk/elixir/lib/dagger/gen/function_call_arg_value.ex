# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.FunctionCallArgValue do
  @moduledoc "A value passed as a named argument to a function call."
  use Dagger.Core.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "A unique identifier for this FunctionCallArgValue."
    @spec id(t()) :: {:ok, Dagger.FunctionCallArgValueID.t()} | {:error, term()}
    def id(%__MODULE__{} = function_call_arg_value) do
      selection = select(function_call_arg_value.selection, "id")
      execute(selection, function_call_arg_value.client)
    end
  )

  (
    @doc "The name of the argument."
    @spec name(t()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def name(%__MODULE__{} = function_call_arg_value) do
      selection = select(function_call_arg_value.selection, "name")
      execute(selection, function_call_arg_value.client)
    end
  )

  (
    @doc "The value of the argument represented as a JSON serialized string."
    @spec value(t()) :: {:ok, Dagger.JSON.t()} | {:error, term()}
    def value(%__MODULE__{} = function_call_arg_value) do
      selection = select(function_call_arg_value.selection, "value")
      execute(selection, function_call_arg_value.client)
    end
  )
end
