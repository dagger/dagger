defmodule Dagger.GlobalTest do
  use ExUnit.Case, async: true

  test "dag/0" do
    start_supervised!(Dagger.Global)
    assert %Dagger.Client{} = Dagger.Global.dag()
  end
end
