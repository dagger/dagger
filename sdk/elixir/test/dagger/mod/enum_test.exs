defmodule Dagger.Mod.EnumTest do
  use ExUnit.Case, async: true

  test "get possible values" do
    assert SimpleEnum.__enum__(:keys) == [:unknown, :low, :high]
  end

  test "get value of the enum" do
    assert_enum = fn module ->
      assert Enum.map([:unknown, :low, :high], &module.__enum__(:value, &1)) == [
               "unknown",
               "low",
               "high"
             ]
    end

    assert_enum.(SimpleEnum)
    assert_enum.(EnumWithOption)
  end

  test "alias key with value" do
    assert EnumAliasValue.__enum__(:value, :UNKNOWN) == "unknown"
    assert EnumAliasValue.__enum__(:value, :LOW) == "low"
  end
end
