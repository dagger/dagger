defmodule Dagger.Mod.ObjectTest do
  use ExUnit.Case, async: true

  test "store function information" do
    defmodule A do
      use Dagger.Mod.Object, name: "A"

      function([name: String.t()], String.t())

      def accept_string(args) do
        "Hello, #{args.name}"
      end

      function([name: binary()], binary())

      def accept_string2(args) do
        "Hello, #{args.name}"
      end

      function([name: integer()], binary())

      def accept_integer(args) do
        "Hello, #{args.name}"
      end

      function([name: boolean()], binary())

      def accept_boolean(args) do
        "Hello, #{args.name}"
      end

      function([], String.t())

      def empty_args(_args) do
        "Empty args"
      end

      function([name: Dagger.Container.t()], Dagger.Container.t())

      def accept_and_return_module(_args) do
        nil
      end

      function([name: list(String.t())], String.t())

      def accept_list(_args) do
        "Accept list"
      end

      function([name: [String.t()]], String.t())

      def accept_list2(_args) do
        "Accept list"
      end
    end

    assert functions_for(A) == [
             accept_list2: [
               args: [name: [type: {:list, :string}]],
               return: :string
             ],
             accept_list: [
               args: [name: [type: {:list, :string}]],
               return: :string
             ],
             accept_and_return_module: [
               args: [name: [type: Dagger.Container]],
               return: Dagger.Container
             ],
             empty_args: [
               args: [],
               return: :string
             ],
             accept_boolean: [
               args: [name: [type: :boolean]],
               return: :string
             ],
             accept_integer: [
               args: [name: [type: :integer]],
               return: :string
             ],
             accept_string2: [
               args: [name: [type: :string]],
               return: :string
             ],
             accept_string: [
               args: [name: [type: :string]],
               return: :string
             ]
           ]
  end

  test "throw unsupported type" do
    assert_raise ArgumentError, "type `non_neg_integer()` is not supported", fn ->
      defmodule ShouldThrowError do
        use Dagger.Mod.Object, name: "ShouldThrowError"

        function([name: non_neg_integer()], String.t())

        def accept_string(args) do
          "Hello, #{args.name}"
        end
      end
    end
  end

  test "raise with define a function with defp" do
    assert_raise RuntimeError, fn ->
      defmodule RaiseDefp do
        use Dagger.Mod.Object, name: "RaiseDefp"

        function([], String.t())
        defp hello(_args), do: "It works"

        def dummy(), do: hello(nil, %{})
      end
    end
  end

  test "raise when define with function != 2 arities" do
    assert_raise RuntimeError, fn ->
      defmodule RaiseArityError do
        use Dagger.Mod.Object, name: "RaiseArityError"

        function([], String.t())
        def hello(_args, _opts), do: "It works"
      end
    end
  end

  test "store the module name" do
    defmodule C do
      use Dagger.Mod.Object, name: "C"

      function([name: String.t()], String.t())

      def hello(args) do
        "Hello, #{args.name}"
      end
    end

    assert name_for(C) == "C"
  end

  defp name_for(module) do
    Dagger.Mod.Module.name_for(module)
  end

  defp functions_for(module) do
    Dagger.Mod.Module.functions_for(module)
  end
end
