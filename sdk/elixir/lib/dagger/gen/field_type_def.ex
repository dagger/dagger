# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.FieldTypeDef do
  @moduledoc """
  A definition of a field on a custom object defined in a Module.

  A field on an object has a static value, as opposed to a function on an object whose value is computed by invoking code (and can accept arguments).
  """

  use Dagger.Core.QueryBuilder

  @derive Dagger.ID

  defstruct [:selection, :client]

  @type t() :: %__MODULE__{}

  @doc "A doc string for the field, if any."
  @spec description(t()) :: {:ok, String.t()} | {:error, term()}
  def description(%__MODULE__{} = field_type_def) do
    selection =
      field_type_def.selection |> select("description")

    execute(selection, field_type_def.client)
  end

  @doc "A unique identifier for this FieldTypeDef."
  @spec id(t()) :: {:ok, Dagger.FieldTypeDefID.t()} | {:error, term()}
  def id(%__MODULE__{} = field_type_def) do
    selection =
      field_type_def.selection |> select("id")

    execute(selection, field_type_def.client)
  end

  @doc "The name of the field in lowerCamelCase format."
  @spec name(t()) :: {:ok, String.t()} | {:error, term()}
  def name(%__MODULE__{} = field_type_def) do
    selection =
      field_type_def.selection |> select("name")

    execute(selection, field_type_def.client)
  end

  @doc "The type of the field."
  @spec type_def(t()) :: Dagger.TypeDef.t()
  def type_def(%__MODULE__{} = field_type_def) do
    selection =
      field_type_def.selection |> select("typeDef")

    %Dagger.TypeDef{
      selection: selection,
      client: field_type_def.client
    }
  end
end
