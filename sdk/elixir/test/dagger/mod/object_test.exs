defmodule Dagger.Mod.ObjectTest do
  use ExUnit.Case, async: true

  describe "defn/2" do
    test "store function information" do
      assert ObjectMod.__object__(:functions) == [
               accept_string: [
                 self: false,
                 args: [
                   name: [{:ignore, nil}, {:default_path, nil}, {:doc, nil}, {:type, :string}]
                 ],
                 return: :string
               ],
               accept_string2: [
                 self: false,
                 args: [
                   name: [{:ignore, nil}, {:default_path, nil}, {:doc, nil}, {:type, :string}]
                 ],
                 return: :string
               ],
               accept_integer: [
                 self: false,
                 args: [
                   name: [{:ignore, nil}, {:default_path, nil}, {:doc, nil}, {:type, :integer}]
                 ],
                 return: :integer
               ],
               accept_boolean: [
                 self: false,
                 args: [
                   name: [{:ignore, nil}, {:default_path, nil}, {:doc, nil}, {:type, :boolean}]
                 ],
                 return: :string
               ],
               empty_args: [self: false, args: [], return: :string],
               accept_and_return_module: [
                 self: false,
                 args: [
                   container: [
                     {:ignore, nil},
                     {:default_path, nil},
                     {:doc, nil},
                     {:type, Dagger.Container}
                   ]
                 ],
                 return: Dagger.Container
               ],
               accept_list: [
                 self: false,
                 args: [
                   alist: [
                     {:ignore, nil},
                     {:default_path, nil},
                     {:doc, nil},
                     {:type, {:list, :string}}
                   ]
                 ],
                 return: :string
               ],
               accept_list2: [
                 self: false,
                 args: [
                   alist: [
                     {:ignore, nil},
                     {:default_path, nil},
                     {:doc, nil},
                     {:type, {:list, :string}}
                   ]
                 ],
                 return: :string
               ],
               optional_arg: [
                 self: false,
                 args: [
                   s: [
                     {:ignore, nil},
                     {:default_path, nil},
                     {:doc, nil},
                     {:type, {:optional, :string}}
                   ]
                 ],
                 return: :string
               ],
               type_option: [
                 self: false,
                 args: [
                   dir: [
                     {:ignore, ["deps", "_build"]},
                     {:default_path, "/sdk/elixir"},
                     {:doc, "The directory to run on."},
                     {:type, {:optional, Dagger.Directory}}
                   ]
                 ],
                 return: :string
               ],
               return_void: [self: false, args: [], return: Dagger.Void]
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

    test "type option validation" do
      assert_raise FunctionClauseError, fn ->
        defmodule TypeOptDoc do
          use Dagger.Mod.Object, name: "TypeOptDoc"

          defn should_fail(v: {Dagger.String.t(), doc: 1}) :: String.t() do
            v
          end
        end
      end

      assert_raise FunctionClauseError, fn ->
        defmodule TypeOptDefaultPath do
          use Dagger.Mod.Object, name: "TypeOptDoc"

          defn should_fail(v: {Dagger.String.t(), default_path: 1}) :: String.t() do
            v
          end
        end
      end

      assert_raise FunctionClauseError, fn ->
        defmodule TypeOptIgnore do
          use Dagger.Mod.Object, name: "TypeOptDoc"

          defn should_fail(v: {Dagger.String.t(), ignore: 1}) :: String.t() do
            v
          end
        end
      end
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
