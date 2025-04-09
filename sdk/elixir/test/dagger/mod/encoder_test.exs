defmodule Dagger.Mod.EncoderTest do
  use Dagger.DagCase

  alias Dagger.Mod.Encoder

  describe "validate_and_encode/2" do
    test "encode primitive type" do
      assert {:ok, "\"hello\""} = Encoder.validate_and_encode("hello", :string)
      assert {:ok, "1"} = Encoder.validate_and_encode(1, :integer)
      assert {:ok, "2.0"} = Encoder.validate_and_encode(2.0, :float)
      assert {:ok, "true"} = Encoder.validate_and_encode(true, :boolean)
      assert {:ok, "false"} = Encoder.validate_and_encode(false, :boolean)
    end

    test "encode list", %{dag: dag} do
      assert {:ok, "[1,2,3]"} = Encoder.validate_and_encode([1, 2, 3], {:list, :integer})
    end

    test "encode idable module", %{dag: dag} do
      assert {:ok, id} =
               Encoder.validate_and_encode(Dagger.Client.container(dag), Dagger.Container)

      assert is_binary(id)
    end

    test "encode void type", %{dag: dag} do
      assert {:ok, "null"} = Encoder.validate_and_encode("hello", Dagger.Void)
      assert {:ok, "null"} = Encoder.validate_and_encode(1, Dagger.Void)
      assert {:ok, "null"} = Encoder.validate_and_encode(:ok, Dagger.Void)
    end

    test "encode object", %{dag: dag} do
      assert {:ok, "{\"name\":\"john\"}"} =
               Encoder.validate_and_encode(%ObjectField{name: "john"}, ObjectField)
    end

    test "encode error", %{dag: dag} do
      assert {:error, _} = Encoder.validate_and_encode(1, :string)
    end
  end
end
