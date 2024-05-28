defmodule Dagger.ModTest do
  use ExUnit.Case
  doctest Dagger.Mod

  alias Dagger.Mod

  test "store function information" do
    defmodule A do
      use Dagger.Mod, name: "A"

      @function [
        args: [
          name: [type: :string]
        ],
        return: :string
      ]
      def hello(_self, args) do
        "Hello, #{args.name}"
      end
    end

    assert functions_for(A) == [
             hello: [
               args: [
                 name: [type: :string]
               ],
               return: :string
             ]
           ]

    defmodule B do
      use Dagger.Mod, name: "B"

      @function [
        args: [],
        return: :string
      ]
      def hello(_self, _args), do: "It works"
    end

    assert functions_for(B) == [
             hello: [
               args: [],
               return: :string
             ]
           ]
  end

  test "raise when define with function != 2 arities" do
    assert_raise RuntimeError, fn ->
      defmodule RaiseArityError do
        use Dagger.Mod, name: "RaiseArityError"

        @function [
          args: [],
          return: :string
        ]
        def hello(_self, _args, _opts), do: "It works"
      end
    end
  end

  test "raise when define with defp" do
    assert_raise RuntimeError, fn ->
      defmodule RaiseDefp do
        use Dagger.Mod, name: "RaiseDefp"

        @function [
          args: [],
          return: :string
        ]
        defp hello(_self, _args), do: "It works"

        def dummy(), do: hello(nil, %{})
      end
    end
  end

  test "store the module name" do
    defmodule C do
      use Dagger.Mod, name: "C"

      @function [
        args: [
          name: [type: :string]
        ],
        return: :string
      ]
      def hello(_self, args) do
        "Hello, #{args.name}"
      end
    end

    assert name_for(C) == "C"
  end

  test "missing args in function declarattion" do
    assert_raise NimbleOptions.ValidationError, fn ->
      defmodule NoArgsModule do
        use Dagger.Mod, name: "NoArgsModule"

        @function [
          return: :string
        ]
        def hello(_self, _args) do
          "Hello"
        end
      end
    end
  end

  test "missing return in function declarattion" do
    assert_raise NimbleOptions.ValidationError, fn ->
      defmodule NoTypeModule do
        use Dagger.Mod, name: "NoTypeModule"

        @function [
          args: []
        ]
        def hello(_self, _args) do
          "Hello"
        end
      end
    end
  end

  test "decode/2" do
    dag = Dagger.connect!()
    on_exit(fn -> Dagger.close(dag) end)

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

  test "encode/2" do
    dag = Dagger.connect!()
    on_exit(fn -> Dagger.close(dag) end)

    assert {:ok, "\"hello\""} = Mod.encode("hello", :string)
    assert {:ok, "1"} = Mod.encode(1, :integer)
    assert {:ok, "true"} = Mod.encode(true, :boolean)
    assert {:ok, "false"} = Mod.encode(false, :boolean)
    assert {:ok, "[1,2,3]"} = Mod.encode([1, 2, 3], {:list, :integer})
    assert {:ok, id} = Mod.encode(Dagger.Client.container(dag), Dagger.Container)
    assert is_binary(id)

    assert {:error, _} = Mod.encode(1, :string)
  end

  defp name_for(module) do
    Dagger.Mod.Module.name_for(module)
  end

  defp functions_for(module) do
    Dagger.Mod.Module.functions_for(module)
  end
end
