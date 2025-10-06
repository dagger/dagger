defmodule Dagger.Codegen.Introspection.Types.Directive.Arg do
  defstruct [:name, :value]

  def from_map(%{"name" => name, "value" => value}) do
    %__MODULE__{name: name, value: value}
  end
end

defmodule Dagger.Codegen.Introspection.Types.Directive do
  defstruct [:name, :args]

  def from_map(%{"name" => name, "args" => args}) do
    %__MODULE__{name: name, args: Enum.map(args, &__MODULE__.Arg.from_map/1)}
  end
end
