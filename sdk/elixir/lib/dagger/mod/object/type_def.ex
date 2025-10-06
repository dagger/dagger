defmodule Dagger.Mod.Object.TypeDef do
  @moduledoc false

  # A set of functions for working with type system in the SDK.

  @doc """
  Define a Dagger TypeDef from `type`.
  """
  def define(dag, type) do
    define(dag, Dagger.Client.type_def(dag), type)
  end

  def define(_dag, type_def, :integer) do
    type_def
    |> Dagger.TypeDef.with_kind(Dagger.TypeDefKind.integer_kind())
  end

  def define(_dag, type_def, :float) do
    type_def
    |> Dagger.TypeDef.with_kind(Dagger.TypeDefKind.float_kind())
  end

  def define(_dag, type_def, :boolean) do
    type_def
    |> Dagger.TypeDef.with_kind(Dagger.TypeDefKind.boolean_kind())
  end

  def define(_dag, type_def, :string) do
    type_def
    |> Dagger.TypeDef.with_kind(Dagger.TypeDefKind.string_kind())
  end

  def define(dag, type_def, {:list, type}) do
    type_def
    |> Dagger.TypeDef.with_list_of(define(dag, type))
  end

  def define(dag, type_def, {:optional, type}) do
    dag
    |> define(type_def, type)
    |> Dagger.TypeDef.with_optional(true)
  end

  def define(_dag, type_def, module) do
    name = module.__name__()

    case module.__kind__() do
      :object ->
        Dagger.TypeDef.with_object(type_def, name)

      :scalar ->
        if name == "Void" do
          type_def
          |> Dagger.TypeDef.with_kind(Dagger.TypeDefKind.void_kind())
          |> Dagger.TypeDef.with_optional(true)
        else
          Dagger.TypeDef.with_scalar(type_def, name)
        end

      :enum ->
        Dagger.TypeDef.with_enum(type_def, name)
    end
  end
end
