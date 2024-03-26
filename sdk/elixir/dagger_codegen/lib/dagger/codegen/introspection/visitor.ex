defmodule Dagger.Codegen.Introspection.VisitorHandlers do
  defstruct [:scalar, :object, :input, :enum]
end

defmodule Dagger.Codegen.Introspection.Visitor do
  alias Dagger.Codegen.Introspection.VisitorHandlers, as: VH

  def visit(schema, %VH{} = handlers) do
    [
      {"SCALAR", handlers.scalar, ["String", "Float", "Int", "Boolean", "DateTime", "ID"]},
      {"INPUT_OBJECT", handlers.input, []},
      {"OBJECT", handlers.object, []},
      {"ENUM", handlers.enum, []}
    ]
    |> Enum.map(&visit(schema, &1))
  end

  def visit(schema, {kind, handler, ignore}) do
    schema.types
    |> Stream.filter(&(&1.kind == kind))
    |> Stream.reject(&String.starts_with?(&1.name, "__"))
    |> Stream.reject(&(&1.name in ignore))
    |> Stream.map(fn type ->
      %{
        type
        | fields:
            if type.fields do
              Enum.sort_by(type.fields, & &1.name)
            else
              nil
            end,
          input_fields:
            if type.input_fields do
              Enum.sort_by(type.input_fields, & &1.name)
            else
              nil
            end
      }
    end)
    |> Enum.sort_by(& &1.name)
    |> Enum.map(handler)
  end
end
