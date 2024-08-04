# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.Function do
  @moduledoc """
  Function represents a resolver provided by a Module.

  A function always evaluates against a parent object and is given a set of named arguments.
  """

  alias Dagger.Core.Client
  alias Dagger.Core.QueryBuilder, as: QB

  @derive Dagger.ID

  defstruct [:query_builder, :client]

  @type t() :: %__MODULE__{}

  @doc "Arguments accepted by the function, if any."
  @spec args(t()) :: {:ok, [Dagger.FunctionArg.t()]} | {:error, term()}
  def args(%__MODULE__{} = function) do
    query_builder =
      function.query_builder |> QB.select("args") |> QB.select("id")

    with {:ok, items} <- Client.execute(function.client, query_builder) do
      {:ok,
       for %{"id" => id} <- items do
         %Dagger.FunctionArg{
           query_builder:
             QB.query()
             |> QB.select("loadFunctionArgFromID")
             |> QB.put_arg("id", id),
           client: function.client
         }
       end}
    end
  end

  @doc "A doc string for the function, if any."
  @spec description(t()) :: {:ok, String.t()} | {:error, term()}
  def description(%__MODULE__{} = function) do
    query_builder =
      function.query_builder |> QB.select("description")

    Client.execute(function.client, query_builder)
  end

  @doc "A unique identifier for this Function."
  @spec id(t()) :: {:ok, Dagger.FunctionID.t()} | {:error, term()}
  def id(%__MODULE__{} = function) do
    query_builder =
      function.query_builder |> QB.select("id")

    Client.execute(function.client, query_builder)
  end

  @doc "The name of the function."
  @spec name(t()) :: {:ok, String.t()} | {:error, term()}
  def name(%__MODULE__{} = function) do
    query_builder =
      function.query_builder |> QB.select("name")

    Client.execute(function.client, query_builder)
  end

  @doc "The type returned by the function."
  @spec return_type(t()) :: Dagger.TypeDef.t()
  def return_type(%__MODULE__{} = function) do
    query_builder =
      function.query_builder |> QB.select("returnType")

    %Dagger.TypeDef{
      query_builder: query_builder,
      client: function.client
    }
  end

  @doc "Returns the function with the provided argument"
  @spec with_arg(t(), String.t(), Dagger.TypeDef.t(), [
          {:description, String.t() | nil},
          {:default_value, Dagger.JSON.t() | nil}
        ]) :: Dagger.Function.t()
  def with_arg(%__MODULE__{} = function, name, type_def, optional_args \\ []) do
    query_builder =
      function.query_builder
      |> QB.select("withArg")
      |> QB.put_arg("name", name)
      |> QB.put_arg("typeDef", Dagger.ID.id!(type_def))
      |> QB.maybe_put_arg("description", optional_args[:description])
      |> QB.maybe_put_arg("defaultValue", optional_args[:default_value])

    %Dagger.Function{
      query_builder: query_builder,
      client: function.client
    }
  end

  @doc "Returns the function with the given doc string."
  @spec with_description(t(), String.t()) :: Dagger.Function.t()
  def with_description(%__MODULE__{} = function, description) do
    query_builder =
      function.query_builder
      |> QB.select("withDescription")
      |> QB.put_arg("description", description)

    %Dagger.Function{
      query_builder: query_builder,
      client: function.client
    }
  end
end
