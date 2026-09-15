defmodule SimpleEnum do
  @moduledoc false
  use Dagger.Mod.Enum, name: "SimpleEnum", values: [:unknown, :low, :high]
end

defmodule EnumWithOption do
  @moduledoc false
  use Dagger.Mod.Enum,
    name: "EnumWithOption",
    values: [:low, :high, unknown: [doc: "Unknown severity"]]
end

defmodule EnumAliasValue do
  @moduledoc false
  use Dagger.Mod.Enum,
    name: "EnumAliasValue",
    values: [UNKNOWN: "unknown", LOW: {"low", doc: "Low severity"}]
end
