defmodule Dagger.Mod.ObjectTest do
  use ExUnit.Case, async: true

  describe "defn/2" do
    test "store function information" do
      defmodule A do
        use Dagger.Mod.Object, name: "A"

        defn accept_string(name: String.t()) :: String.t() do
          "Hello, #{name}"
        end

        defn accept_string2(name: binary()) :: binary() do
          "Hello, #{name}"
        end

        defn accept_integer(name: integer()) :: integer() do
          "Hello, #{name}"
        end

        defn accept_boolean(name: boolean()) :: String.t() do
          "Hello, #{name}"
        end

        defn empty_args() :: String.t() do
          "Empty args"
        end

        defn accept_and_return_module(container: Dagger.Container.t()) :: Dagger.Container.t() do
          container
        end

        defn accept_list(alist: list(String.t())) :: String.t() do
          Enum.join(alist, ",")
        end

        defn accept_list2(alist: [String.t()]) :: String.t() do
          Enum.join(alist, ",")
        end
      end

      assert A.__object__(:functions) == [
               accept_string: [
                 {:self, false},
                 {:args, [name: [type: :string]]},
                 {:return, :string}
               ],
               accept_string2: [
                 {:self, false},
                 {:args, [name: [type: :string]]},
                 {:return, :string}
               ],
               accept_integer: [
                 {:self, false},
                 {:args, [name: [type: :integer]]},
                 {:return, :integer}
               ],
               accept_boolean: [
                 {:self, false},
                 {:args, [name: [type: :boolean]]},
                 {:return, :string}
               ],
               empty_args: [{:self, false}, {:args, []}, {:return, :string}],
               accept_and_return_module: [
                 {:self, false},
                 {:args, [container: [type: Dagger.Container]]},
                 {:return, Dagger.Container}
               ],
               accept_list: [
                 {:self, false},
                 {:args, [alist: [type: {:list, :string}]]},
                 {:return, :string}
               ],
               accept_list2: [
                 {:self, false},
                 {:args, [alist: [type: {:list, :string}]]},
                 {:return, :string}
               ]
             ]
    end

    test "throw unsupported type" do
      assert_raise ArgumentError, "type `non_neg_integer()` is not supported", fn ->
        defmodule ShouldThrowError do
          use Dagger.Mod.Object, name: "ShouldThrowError"

          defn accept_string(name: non_neg_integer()) :: String.t() do
            "Hello, #{name}"
          end
        end
      end
    end

    test "store the module name" do
      defmodule C do
        use Dagger.Mod.Object, name: "C"

        defn hello(name: String.t()) :: String.t() do
          "Hello, #{name}"
        end
      end

      assert C.__object__(:name) == "C"
    end

    test "typespec" do
      {:ok, specs} = Code.Typespec.fetch_specs(DocModule)

      fun_specs =
        Enum.flat_map(specs, fn {{name, _}, specs} ->
          specs
          |> Enum.map(fn spec -> Code.Typespec.spec_to_quoted(name, spec) end)
          |> Enum.map(fn spec -> quote(do: @spec(unquote(spec))) end)
          |> Enum.map(&Macro.to_string/1)
          |> Enum.sort()
        end)

      assert fun_specs == [
               "@spec no_fun_doc() :: String.t()",
               "@spec hidden_fun_doc() :: String.t()",
               "@spec echo(name :: String.t()) :: String.t()"
             ]
    end
  end

  test "get_module_doc/1" do
    assert Dagger.Mod.Object.get_module_doc(DocModule) == "The module documentation."
    assert is_nil(Dagger.Mod.Object.get_module_doc(NoDocModule))
    assert is_nil(Dagger.Mod.Object.get_module_doc(HiddenDocModule))
  end

  test "get_function_doc/2" do
    assert Dagger.Mod.Object.get_function_doc(DocModule, :echo) == "Echo the output."
    assert is_nil(Dagger.Mod.Object.get_function_doc(DocModule, :no_fun_doc))
    assert is_nil(Dagger.Mod.Object.get_function_doc(DocModule, :hidden_fun_doc))
  end
end
