defmodule Dagger.ModuleTest do
  use Dagger.Case, async: true

  import Dagger.TestHelper

  test "init from scratch", %{dag: dag} do
    assert {:ok, output} =
             dag
             |> dagger_cli_base()
             |> dagger_init()
             |> dagger_call(["container-echo", "--string-arg", "hello", "stdout"])
             |> stdout()

    assert output == "hello\n"
  end

  test "init with different root", %{dag: dag} do
    assert {:ok, output} =
             dag
             |> dagger_cli_base()
             |> dagger_init("child", [])
             |> dagger_call("child", ["container-echo", "--string-arg", "hello", "stdout"])
             |> stdout()

    assert output == "hello\n"
  end

  test "run init and develop", %{dag: dag} do
    assert {:ok, output} =
             dag
             |> dagger_cli_base()
             |> dagger_exec(["init"])
             |> dagger_exec(["develop", "--sdk=#{sdk()}", "--source=."])
             |> dagger_call(["container-echo", "--string-arg", "hello", "stdout"])
             |> stdout()

    assert output == "hello\n"
  end
end
