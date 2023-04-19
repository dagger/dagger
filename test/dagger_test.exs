defmodule DaggerTest do
  use ExUnit.Case
  doctest Dagger

  test "greets the world" do
    assert Dagger.hello() == :world
  end
end
