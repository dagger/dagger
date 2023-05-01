defmodule Dagger.Codegen.Elixir.Templates.EnumTmpl do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Function

  def render_enum(%{"name" => name, "description" => desc, "enumValues" => enum_values}) do
    mod_name = Module.concat([Dagger, Function.format_module_name(name)])

    funs =
      for %{
            "name" => value,
            "description" => desc,
            "deprecationReason" => deprecated_reason
          } <-
            enum_values do
        Function.define(
          value,
          [],
          nil,
          quote do
            unquote(value)
          end,
          doc: desc,
          deprecated: deprecated_reason
        )
      end

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)

        unquote_splicing(funs)
      end
    end
  end
end
