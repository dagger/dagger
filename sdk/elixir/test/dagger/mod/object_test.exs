defmodule Dagger.Mod.ObjectTest do
  use ExUnit.Case, async: true

  test "store function information" do
    defmodule A do
      use Dagger.Mod.Object

      function([name: String.t()], String.t())

      def accept_string(_dag, args) do
        "Hello, #{args.name}"
      end

      function([name: binary()], binary())

      def accept_string2(_dag, args) do
        "Hello, #{args.name}"
      end

      function([name: integer()], binary())

      def accept_integer(_dag, args) do
        "Hello, #{args.name}"
      end

      function([name: boolean()], binary())

      def accept_boolean(_dag, args) do
        "Hello, #{args.name}"
      end

      function([], String.t())

      def empty_args(_dag, _args) do
        "Empty args"
      end

      function([name: Dagger.Container.t()], Dagger.Container.t())

      def accept_and_return_module(dag, _args) do
        dag
        |> Dagger.Client.container()
      end

      function([name: list(String.t())], String.t())

      def accept_list(_dag, _args) do
        "Accept list"
      end

      function([name: [String.t()]], String.t())

      def accept_list2(_dag, _args) do
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
        use Dagger.Mod.Object

        function([name: non_neg_integer()], String.t())

        def accept_string(_dag, args) do
          "Hello, #{args.name}"
        end
      end
    end
  end

  defp functions_for(module) do
    Dagger.Mod.Module.functions_for(module)
  end
end
