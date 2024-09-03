defmodule Dagger.Codegen.Introspection.Types.TypeRef do
  defstruct [
    :kind,
    :name,
    :of_type
  ]

  def from_map(%{"kind" => kind} = type_ref) do
    %__MODULE__{
      kind: kind,
      name: type_ref["name"],
      of_type:
        case type_ref["ofType"] do
          nil -> nil
          :null -> nil
          of_type -> Dagger.Codegen.Introspection.Types.TypeRef.from_map(of_type)
        end
    }
  end

  def is_scalar?(%__MODULE__{kind: "NON_NULL", of_type: type}), do: is_scalar?(type)
  def is_scalar?(%__MODULE__{kind: "SCALAR"}), do: true
  def is_scalar?(_), do: false

  def is_enum?(%__MODULE__{kind: "NON_NULL", of_type: type}), do: is_enum?(type)
  def is_enum?(%__MODULE__{kind: "ENUM"}), do: true
  def is_enum?(_), do: false

  def is_void?(%__MODULE__{kind: "NON_NULL", of_type: type}), do: is_void?(type)
  def is_void?(%__MODULE__{kind: "SCALAR", name: "Void"}), do: true
  def is_void?(_), do: false

  def is_list_of?(%__MODULE__{kind: "NON_NULL", of_type: type}, of_kind),
    do: is_list_of?(type, of_kind)

  def is_list_of?(
        %__MODULE__{
          kind: "LIST",
          of_type: %__MODULE__{kind: "NON_NULL", of_type: %__MODULE__{kind: of_kind}}
        },
        of_kind
      ),
      do: true

  def is_list_of?(_, _), do: false

  # TODO: refactor me.
  def unwrap_list(%__MODULE__{
        kind: "NON_NULL",
        of_type: %__MODULE__{
          kind: "LIST",
          of_type: %__MODULE__{
            kind: "NON_NULL",
            of_type: type
          }
        }
      }) do
    type
  end

  def unwrap_list(%__MODULE__{
        kind: "LIST",
        of_type: %__MODULE__{
          kind: "NON_NULL",
          of_type: type
        }
      }) do
    type
  end

  def id_type?(%__MODULE__{kind: "NON_NULL", of_type: type}), do: id_type?(type)
  def id_type?(%__MODULE__{kind: "SCALAR", name: name}), do: String.ends_with?(name, "ID")
  def id_type?(_), do: false
end
