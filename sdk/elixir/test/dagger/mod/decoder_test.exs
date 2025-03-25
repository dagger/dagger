defmodule Dagger.Mod.DecoderTest do
  use Dagger.DagCase

  alias Dagger.Mod.Decoder

  describe "decode/2" do
    test "decode primitive type", %{dag: dag} do
      assert {:ok, "hello"} = Decoder.decode(json("hello"), :string, dag)
      assert {:ok, 1} = Decoder.decode(json(1), :integer, dag)
      assert {:ok, 2.0} = Decoder.decode(json(2.0), :float, dag)
      assert {:ok, true} = Decoder.decode(json(true), :boolean, dag)
      assert {:ok, false} = Decoder.decode(json(false), :boolean, dag)
    end

    test "decode list", %{dag: dag} do
      assert {:ok, [1, 2, 3]} =
               Decoder.decode(json([1, 2, 3]), {:list, :integer}, dag)
    end

    test "decode optional", %{dag: dag} do
      assert {:ok, nil} = Decoder.decode(nil, {:optional, :string}, dag)
      assert {:ok, "hello"} = Decoder.decode(json("hello"), {:optional, :string}, dag)
    end

    test "decode id to struct", %{dag: dag} do
      assert {:ok, %Dagger.Container{} = container} =
               Decoder.decode(json(container_id(dag)), Dagger.Container, dag)

      # Ensure the client (`dag`) is passing through the Nestru correctly.
      assert {:ok, _} = Dagger.Container.sync(container)
    end

    test "decode error", %{dag: dag} do
      assert {:error, _} = Decoder.decode(json(1), :string, dag)
    end
  end

  defp json(value), do: Jason.encode!(value)

  defp container_id(dag) do
    {:ok, container_id} = dag |> Dagger.Client.container() |> Dagger.Container.id()
    container_id
  end
end
