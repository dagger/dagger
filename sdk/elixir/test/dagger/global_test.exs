defmodule Dagger.GlobalTest do
  use Dagger.DagCase

  test "dag/0" do
    assert %Dagger.Client{} = Dagger.Global.dag()
  end
end
