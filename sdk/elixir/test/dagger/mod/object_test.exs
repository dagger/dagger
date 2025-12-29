defmodule Dagger.Mod.ObjectTest do
  use ExUnit.Case, async: true

  alias Dagger.Mod.Object.FunctionDef
  alias Dagger.Mod.Object.FieldDef

  describe "defn/2" do
    test "primitive type arguments" do
      assert PrimitiveTypeArgs.__object__(:functions) == [
               accept_string: %FunctionDef{
                 self: false,
                 args: [
                   name: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, :string}
                   ]
                 ],
                 return: :string
               },
               accept_string2: %FunctionDef{
                 self: false,
                 args: [
                   name: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, :string}
                   ]
                 ],
                 return: :string
               },
               accept_integer: %FunctionDef{
                 self: false,
                 args: [
                   value: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, :integer}
                   ]
                 ],
                 return: :integer
               },
               accept_float: %FunctionDef{
                 self: false,
                 args: [
                   value: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, :float}
                   ]
                 ],
                 return: :float
               },
               accept_boolean: %FunctionDef{
                 self: false,
                 args: [
                   name: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, :boolean}
                   ]
                 ],
                 return: :string
               }
             ]
    end

    test "primitive type default arguments" do
      assert PrimitiveTypeDefaultArgs.__object__(:functions) == [
               accept_default_string: %FunctionDef{
                 self: false,
                 args: [
                   name: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:doc, nil},
                     {:default, "Foo"},
                     {:type, {:optional, :string}}
                   ]
                 ],
                 return: :string
               },
               accept_default_integer: %FunctionDef{
                 self: false,
                 args: [
                   value: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:doc, nil},
                     {:default, 42},
                     {:type, :integer}
                   ]
                 ],
                 return: :integer
               },
               accept_default_float: %FunctionDef{
                 self: false,
                 args: [
                   value: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:doc, nil},
                     {:default, 1.6180342},
                     {:type, :float}
                   ]
                 ],
                 return: :float
               },
               accept_default_boolean: %FunctionDef{
                 self: false,
                 args: [
                   value: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:doc, nil},
                     {:default, false},
                     {:type, :boolean}
                   ]
                 ],
                 return: :boolean
               }
             ]
    end

    test "empty arguments" do
      assert EmptyArgs.__object__(:functions) == [
               empty_args: %FunctionDef{self: false, args: [], return: :string}
             ]
    end

    test "accept and return object" do
      assert ObjectArgAndReturn.__object__(:functions) == [
               accept_and_return_module: %FunctionDef{
                 self: false,
                 args: [
                   container: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, Dagger.Container}
                   ]
                 ],
                 return: Dagger.Container
               }
             ]
    end

    test "list arguments" do
      assert ListArgs.__object__(:functions) == [
               accept_list: %FunctionDef{
                 self: false,
                 args: [
                   alist: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, {:list, :string}}
                   ]
                 ],
                 return: :string
               },
               accept_list2: %FunctionDef{
                 self: false,
                 args: [
                   alist: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, {:list, :string}}
                   ]
                 ],
                 return: :string
               }
             ]
    end

    test "optional arguments" do
      assert OptionalArgs.__object__(:functions) == [
               optional_arg: %FunctionDef{
                 self: false,
                 args: [
                   s: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, {:optional, :string}}
                   ]
                 ],
                 return: :string
               }
             ]
    end

    test "argument options" do
      assert ArgOptions.__object__(:functions) == [
               type_option: %FunctionDef{
                 self: false,
                 args: [
                   dir: [
                     {:default, nil},
                     {:deprecated, nil},
                     {:ignore, ["deps", "_build"]},
                     {:default_path, "/sdk/elixir"},
                     {:doc, "The directory to run on."},
                     {:type, {:optional, Dagger.Directory}}
                   ]
                 ],
                 return: :string
               }
             ]
    end

    test "return void" do
      assert ReturnVoid.__object__(:functions) == [
               return_void: %FunctionDef{self: false, args: [], return: Dagger.Void}
             ]
    end

    test "self object" do
      assert SelfObject.__object__(:functions) == [
               only_self_arg: %FunctionDef{self: true, args: [], return: Dagger.Void},
               mix_self_and_args: %FunctionDef{
                 self: true,
                 args: [
                   name: [
                     {:ignore, nil},
                     {:deprecated, nil},
                     {:default_path, nil},
                     {:default, nil},
                     {:doc, nil},
                     {:type, :string}
                   ]
                 ],
                 return: Dagger.Void
               }
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

    test "cache policy" do
      assert CacheAttribute.__object__(:functions) == [
               never_cached: %FunctionDef{
                 self: false,
                 args: [],
                 return: Dagger.Void,
                 cache_policy: :never
               },
               per_session_cached: %FunctionDef{
                 self: false,
                 args: [],
                 return: Dagger.Void,
                 cache_policy: :per_session
               },
               ttl_cached: %FunctionDef{
                 self: false,
                 args: [],
                 return: Dagger.Void,
                 cache_policy: [{:ttl, "42s"}]
               }
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

  describe "field/3" do
    test "store fields to a module" do
      assert ObjectField.__object__(:fields) == [
               name: %FieldDef{type: :string, doc: nil}
             ]

      assert ObjectFieldOptional.__object__(:fields) == [
               name: %FieldDef{type: {:optional, :string}, doc: nil}
             ]
    end

    test "required fields" do
      assert struct_keys(%ObjectField{name: "value"}) == [:name]
    end

    test "optional fields" do
      assert struct_keys(%ObjectFieldOptional{}) == [:name]
    end

    test "mixes optional and required fields" do
      assert struct_keys(%ObjectFieldMixesOptionalAndRequired{key: "value"}) == [:name, :key]
    end
  end

  test "mixes object struct and function" do
    assert ObjectFiedAndFunction.__object__(:functions) == [
             with_name: %FunctionDef{
               self: false,
               args: [
                 name: [
                   {:ignore, nil},
                   {:deprecated, nil},
                   {:default_path, nil},
                   {:default, nil},
                   {:doc, nil},
                   {:type, :string}
                 ]
               ],
               return: ObjectFieldAndFunction
             },
             fan_out: %Dagger.Mod.Object.FunctionDef{
               self: false,
               args: [
                 name: [
                   ignore: nil,
                   deprecated: nil,
                   default_path: nil,
                   default: nil,
                   doc: nil,
                   type: :string
                 ]
               ],
               return: {:list, ObjectFieldAndFunction}
             }
           ]
  end

  describe "Deprecation level" do
    test "field deprecation" do
      assert DeprecatedDirective.__object__(:fields) == [
               f1: %FieldDef{type: :string, doc: nil, deprecated: "deprecated field"},
               f2: %FieldDef{type: :string, doc: nil, deprecated: nil}
             ]
    end

    test "function argument deprecation" do
      assert DeprecatedDirective.__object__(:functions)[:deprecated_args] ==
               %Dagger.Mod.Object.FunctionDef{
                 self: false,
                 args: [
                   foo: [
                     {:ignore, nil},
                     {:doc, nil},
                     {:default, nil},
                     {:default_path, nil},
                     {:deprecated, "deprecated argument"},
                     {:type, :string}
                   ],
                   bar: [
                     {:ignore, nil},
                     {:doc, nil},
                     {:default, nil},
                     {:default_path, nil},
                     {:deprecated, nil},
                     {:type, :string}
                   ]
                 ],
                 return: :string
               }
    end
  end

  defp struct_keys(struct), do: struct |> Map.from_struct() |> Map.keys()
end
