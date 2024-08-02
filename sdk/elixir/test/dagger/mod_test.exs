defmodule Dagger.ModTest do
  use ExUnit.Case
  doctest Dagger.Mod

  alias Dagger.Mod

  setup do
    dag = Dagger.connect!()
    on_exit(fn -> Dagger.close(dag) end)
    %{dag: dag}
  end

  test "decode/2", %{dag: dag} do
    assert {:ok, "hello"} = Mod.decode(Jason.encode!("hello"), :string, dag)
    assert {:ok, 1} = Mod.decode(Jason.encode!(1), :integer, dag)
    assert {:ok, true} = Mod.decode(Jason.encode!(true), :boolean, dag)
    assert {:ok, false} = Mod.decode(Jason.encode!(false), :boolean, dag)

    assert {:ok, [1, 2, 3]} =
             Mod.decode(Jason.encode!([1, 2, 3]), {:list, :integer}, dag)

    {:ok, container_id} = dag |> Dagger.Client.container() |> Dagger.Container.id()

    assert {:ok, %Dagger.Container{}} =
             Mod.decode(Jason.encode!(container_id), Dagger.Container, dag)

    assert {:error, _} = Mod.decode(Jason.encode!(1), :string, dag)
  end

  test "encode/2", %{dag: dag} do
    assert {:ok, "\"hello\""} = Mod.encode("hello", :string)
    assert {:ok, "1"} = Mod.encode(1, :integer)
    assert {:ok, "true"} = Mod.encode(true, :boolean)
    assert {:ok, "false"} = Mod.encode(false, :boolean)
    assert {:ok, "[1,2,3]"} = Mod.encode([1, 2, 3], {:list, :integer})
    assert {:ok, id} = Mod.encode(Dagger.Client.container(dag), Dagger.Container)
    assert is_binary(id)

    assert {:error, _} = Mod.encode(1, :string)
  end
end
