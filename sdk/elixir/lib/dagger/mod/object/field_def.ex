defmodule Dagger.Mod.Object.FieldDef do
  @moduledoc false

  # A field definition for declaring a field in the object.

  @enforce_keys [:type, :doc]
  defstruct @enforce_keys ++ [:deprecated]

  @doc """
  Define a Dagger Field from `field_def`.
  """
  def define(%__MODULE__{} = field_def, name, type_def, dag) do
    type_def
    |> Dagger.TypeDef.with_field(
      name,
      Dagger.Mod.Object.TypeDef.define(dag, field_def.type),
      to_field_opts(field_def)
    )
  end

  defp to_field_opts(field_def) do
    opts = []
    opts = Keyword.put(opts, :deprecated, field_def.deprecated)

    if field_def.doc do
      [{:description, field_def.doc} | opts]
    else
      opts
    end
  end
end
