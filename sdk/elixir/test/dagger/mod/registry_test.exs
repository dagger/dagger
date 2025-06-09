defmodule Dagger.Mod.RegistryTest do
  use ExUnit.Case, async: true

  alias Dagger.Mod.Registry

  test "register all returned modules" do
    assert Registry.register(Deps) |> Registry.all_modules() == [Deps, Deps.A, Deps.B, Deps.C]
  end

  test "get module by name" do
    registry = Registry.register(Deps)
    assert Registry.get_module_by_name!(registry, "A") == Deps.A
    assert_raise RuntimeError, fn -> Registry.get_module_by_name!(registry, "NotExist") end
  end

  test "no register itself recursively" do
    registry = Registry.register(ConstructorFunction)
    assert registry |> Registry.all_modules() == [ConstructorFunction]
  end
end
