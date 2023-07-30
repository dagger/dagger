defmodule Dagger.Codegen.Elixir.Templates.Input do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Module, as: Mod

  def render(%{
        "name" => name,
        "description" => desc,
        "inputFields" => fields
      }) do
    mod_name = Mod.from_name(name)

    desc =
      if desc == "" do
        name
      else
        desc
      end

    fields =
      fields
      |> Enum.sort_by(fn %{"name" => name} -> name end)

    struct_fields = Enum.map(fields, &to_struct_field/1)

    data_type_t =
      {:%, [],
       [
         {:__MODULE__, [], Elixir},
         {:%{}, [],
          fields
          |> Enum.map(fn %{"type" => type} = field ->
            type =
              case type do
                %{"kind" => "NON_NULL", "ofType" => type} -> render_type(type)
                type -> render_type(type) |> render_nullable_type()
              end

            {to_struct_field(field), type}
          end)}
       ]}

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)

        @type t() :: unquote(data_type_t)

        @derive Nestru.Decoder
        @derive Jason.Encoder
        defstruct unquote(struct_fields)
      end
    end
  end

  defp to_struct_field(%{"name" => name}) do
    name
    |> Macro.underscore()
    |> String.to_atom()
  end

  defp render_type(%{"kind" => "NON_NULL", "ofType" => type}) do
    render_type(type)
  end

  defp render_type(%{"kind" => "OBJECT", "name" => type}) do
    mod_name = Mod.from_name(type)

    quote do
      unquote(mod_name).t()
    end
  end

  defp render_nullable_type(type) do
    quote do
      unquote(type) | nil
    end
  end

  defp render_type(%{"kind" => "LIST", "ofType" => type}) do
    type = render_type(type)

    quote do
      [unquote(type)]
    end
  end

  defp render_type(%{"kind" => "ENUM", "name" => type}) do
    mod_name = Mod.from_name(type)

    quote do
      unquote(mod_name).t()
    end
  end

  defp render_type(%{"kind" => "SCALAR", "name" => name}) do
    mod_name = Mod.from_name(name)

    quote do
      unquote(mod_name).t()
    end
  end
end
