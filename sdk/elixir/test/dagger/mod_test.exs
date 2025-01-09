defmodule Dagger.ModTest do
  use ExUnit.Case
  doctest Dagger.Mod

  alias Dagger.Mod

  setup_all do
    dag = Dagger.connect!()
    on_exit(fn -> Dagger.close(dag) end)
    %{dag: dag}
  end

  test "decode/2", %{dag: dag} do
    assert {:ok, "hello"} = Mod.decode(json("hello"), :string, dag)
    assert {:ok, 1} = Mod.decode(json(1), :integer, dag)
    assert {:ok, true} = Mod.decode(json(true), :boolean, dag)
    assert {:ok, false} = Mod.decode(json(false), :boolean, dag)

    assert {:ok, [1, 2, 3]} =
             Mod.decode(json([1, 2, 3]), {:list, :integer}, dag)

    assert {:ok, nil} = Mod.decode(json(nil), {:optional, :string}, dag)
    assert {:ok, "hello"} = Mod.decode(json("hello"), {:optional, :string}, dag)

    {:ok, container_id} = dag |> Dagger.Client.container() |> Dagger.Container.id()

    assert {:ok, %Dagger.Container{}} =
             Mod.decode(json(container_id), Dagger.Container, dag)

    assert {:error, _} = Mod.decode(json(1), :string, dag)
  end

  test "encode/2", %{dag: dag} do
    assert {:ok, "\"hello\""} = Mod.encode("hello", :string)
    assert {:ok, "1"} = Mod.encode(1, :integer)
    assert {:ok, "true"} = Mod.encode(true, :boolean)
    assert {:ok, "false"} = Mod.encode(false, :boolean)
    assert {:ok, "[1,2,3]"} = Mod.encode([1, 2, 3], {:list, :integer})
    assert {:ok, id} = Mod.encode(Dagger.Client.container(dag), Dagger.Container)
    assert is_binary(id)
    assert {:ok, "null"} = Mod.encode("hello", Dagger.Void)
    assert {:ok, "null"} = Mod.encode(1, Dagger.Void)
    assert {:ok, "null"} = Mod.encode(:ok, Dagger.Void)

    assert {:error, _} = Mod.encode(1, :string)
  end

  defp json(value) do
    Jason.encode!(value)
  end
end
