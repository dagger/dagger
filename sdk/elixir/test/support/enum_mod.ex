defmodule SimpleEnum do
  @moduledoc false
  use Dagger.Mod.Enum, values: [:unknown, :low, :high]
end

defmodule EnumWithOption do
  @moduledoc false
  use Dagger.Mod.Enum, values: [:low, :high, unknown: [doc: "Unknown severity"]]
end

defmodule EnumAliasValue do
  @moduledoc false
  use Dagger.Mod.Enum, values: [UNKNOWN: "unknown", LOW: {"low", doc: "Low severity"}]
end
