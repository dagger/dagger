# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.FunctionArg do
  @moduledoc "An argument accepted by a function.\n\nThis is a specification for an argument at function definition time, not an\nargument passed at function call time."
  use Dagger.Core.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "A default value to use for this argument when not explicitly set by the caller, if any"
    @spec default_value(t()) :: {:ok, Dagger.JSON.t() | nil} | {:error, term()}
    def default_value(%__MODULE__{} = function_arg) do
      selection = select(function_arg.selection, "defaultValue")
      execute(selection, function_arg.client)
    end
  )

  (
    @doc "A doc string for the argument, if any"
    @spec description(t()) :: {:ok, Dagger.String.t() | nil} | {:error, term()}
    def description(%__MODULE__{} = function_arg) do
      selection = select(function_arg.selection, "description")
      execute(selection, function_arg.client)
    end
  )

  (
    @doc "The ID of the argument"
    @spec id(t()) :: {:ok, Dagger.FunctionArgID.t()} | {:error, term()}
    def id(%__MODULE__{} = function_arg) do
      selection = select(function_arg.selection, "id")
      execute(selection, function_arg.client)
    end
  )

  (
    @doc "The name of the argument"
    @spec name(t()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def name(%__MODULE__{} = function_arg) do
      selection = select(function_arg.selection, "name")
      execute(selection, function_arg.client)
    end
  )

  (
    @doc "The type of the argument"
    @spec type_def(t()) :: Dagger.TypeDef.t()
    def type_def(%__MODULE__{} = function_arg) do
      selection = select(function_arg.selection, "typeDef")
      %Dagger.TypeDef{selection: selection, client: function_arg.client}
    end
  )
end
