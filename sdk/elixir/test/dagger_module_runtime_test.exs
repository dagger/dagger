defmodule Dagger.ModuleRuntimeTest do
  use ExUnit.Case
  doctest Dagger.ModuleRuntime

  alias Dagger.ModuleRuntime

  test "store function information" do
    defmodule A do
      use Dagger.ModuleRuntime, name: "A"

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
      use Dagger.ModuleRuntime, name: "B"

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
        use Dagger.ModuleRuntime, name: "RaiseArityError"

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
        use Dagger.ModuleRuntime, name: "RaiseDefp"

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
      use Dagger.ModuleRuntime, name: "C"

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
        use Dagger.ModuleRuntime, name: "NoArgsModule"

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
        use Dagger.ModuleRuntime, name: "NoTypeModule"

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

    assert {:ok, "hello"} = ModuleRuntime.decode(Jason.encode!("hello"), :string, dag)
    assert {:ok, 1} = ModuleRuntime.decode(Jason.encode!(1), :integer, dag)
    assert {:ok, true} = ModuleRuntime.decode(Jason.encode!(true), :boolean, dag)
    assert {:ok, false} = ModuleRuntime.decode(Jason.encode!(false), :boolean, dag)

    assert {:ok, [1, 2, 3]} =
             ModuleRuntime.decode(Jason.encode!([1, 2, 3]), {:list, :integer}, dag)

    {:ok, container_id} = dag |> Dagger.Client.container() |> Dagger.Container.id()

    assert {:ok, %Dagger.Container{}} =
             ModuleRuntime.decode(Jason.encode!(container_id), Dagger.Container, dag)

    assert {:error, _} = ModuleRuntime.decode(Jason.encode!(1), :string, dag)
  end

  test "encode/2" do
    dag = Dagger.connect!()
    on_exit(fn -> Dagger.close(dag) end)

    assert {:ok, "\"hello\""} = ModuleRuntime.encode("hello", :string)
    assert {:ok, "1"} = ModuleRuntime.encode(1, :integer)
    assert {:ok, "true"} = ModuleRuntime.encode(true, :boolean)
    assert {:ok, "false"} = ModuleRuntime.encode(false, :boolean)
    assert {:ok, "[1,2,3]"} = ModuleRuntime.encode([1, 2, 3], {:list, :integer})
    assert {:ok, id} = ModuleRuntime.encode(Dagger.Client.container(dag), Dagger.Container)
    assert is_binary(id)

    assert {:error, _} = ModuleRuntime.encode(1, :string)
  end

  defp name_for(module) do
    Dagger.ModuleRuntime.Module.name_for(module)
  end

  defp functions_for(module) do
    Dagger.ModuleRuntime.Module.functions_for(module)
  end
end
