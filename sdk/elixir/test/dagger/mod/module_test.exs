defmodule Dagger.Mod.ModuleTest do
  use ExUnit.Case, async: true

  alias Dagger.Mod.Module

  setup_all do
    dag = Dagger.connect!(connect_timeout: :timer.seconds(60))
    on_exit(fn -> Dagger.close(dag) end)

    %{dag: dag}
  end

  describe "define/1" do
    test "primitive type arguments", %{dag: dag} do
      assert {:ok, functions} =
               root_object(dag, PrimitiveTypeArgs) |> Dagger.ObjectTypeDef.functions()

      [accept_string | functions] = functions
      assert {:ok, "acceptString"} = Dagger.Function.name(accept_string)
      assert {:ok, [arg]} = Dagger.Function.args(accept_string)
      assert {:ok, "name"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :STRING_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()

      [accept_string2 | functions] = functions
      assert {:ok, "acceptString2"} = Dagger.Function.name(accept_string2)
      assert {:ok, [arg]} = Dagger.Function.args(accept_string2)
      assert {:ok, "name"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :STRING_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()

      [accept_integer | functions] = functions
      assert {:ok, "acceptInteger"} = Dagger.Function.name(accept_integer)
      assert {:ok, [arg]} = Dagger.Function.args(accept_integer)
      assert {:ok, "value"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :INTEGER_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()

      [accept_float | functions] = functions
      assert {:ok, "acceptFloat"} = Dagger.Function.name(accept_float)
      assert {:ok, [arg]} = Dagger.Function.args(accept_float)
      assert {:ok, "value"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :FLOAT_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()

      [accept_boolean | []] = functions
      assert {:ok, "acceptBoolean"} = Dagger.Function.name(accept_boolean)
      assert {:ok, [arg]} = Dagger.Function.args(accept_boolean)
      assert {:ok, "name"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :BOOLEAN_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()
    end

    test "primitive type default arguments", %{dag: dag} do
      assert {:ok, functions} =
               root_object(dag, PrimitiveTypeDefaultArgs) |> Dagger.ObjectTypeDef.functions()

      [accept_default_string | functions] = functions
      assert {:ok, "acceptDefaultString"} = Dagger.Function.name(accept_default_string)
      assert {:ok, [arg]} = Dagger.Function.args(accept_default_string)
      assert {:ok, "name"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, "\"Foo\""} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :STRING_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()

      [accept_default_integer | functions] = functions
      assert {:ok, "acceptDefaultInteger"} = Dagger.Function.name(accept_default_integer)
      assert {:ok, [arg]} = Dagger.Function.args(accept_default_integer)
      assert {:ok, "value"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, "42"} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :INTEGER_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()

      [accept_default_float | functions] = functions
      assert {:ok, "acceptDefaultFloat"} = Dagger.Function.name(accept_default_float)
      assert {:ok, [arg]} = Dagger.Function.args(accept_default_float)
      assert {:ok, "value"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, "1.6180342"} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :FLOAT_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()

      [accept_default_boolean | []] = functions
      assert {:ok, "acceptDefaultBoolean"} = Dagger.Function.name(accept_default_boolean)
      assert {:ok, [arg]} = Dagger.Function.args(accept_default_boolean)
      assert {:ok, "value"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, "false"} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :BOOLEAN_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()
    end

    test "empty arguments", %{dag: dag} do
      assert {:ok, [empty_args]} =
               root_object(dag, EmptyArgs) |> Dagger.ObjectTypeDef.functions()

      assert {:ok, "emptyArgs"} = Dagger.Function.name(empty_args)
      assert {:ok, []} = Dagger.Function.args(empty_args)
    end

    test "accept and return object", %{dag: dag} do
      assert {:ok, [accept_and_return_module]} =
               root_object(dag, ObjectArgAndReturn) |> Dagger.ObjectTypeDef.functions()

      assert {:ok, "acceptAndReturnModule"} = Dagger.Function.name(accept_and_return_module)
      assert {:ok, [arg]} = Dagger.Function.args(accept_and_return_module)
      assert {:ok, "container"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :OBJECT_KIND} = Dagger.TypeDef.kind(arg_type_def)

      assert {:ok, "Container"} =
               arg_type_def |> Dagger.TypeDef.as_object() |> Dagger.ObjectTypeDef.name()
    end

    test "list arguments", %{dag: dag} do
      assert {:ok, [accept_list, accept_list2]} =
               root_object(dag, ListArgs) |> Dagger.ObjectTypeDef.functions()

      assert {:ok, "acceptList"} = Dagger.Function.name(accept_list)
      assert {:ok, [arg]} = Dagger.Function.args(accept_list)
      assert {:ok, "alist"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :LIST_KIND} = Dagger.TypeDef.kind(arg_type_def)

      sub_type_def =
        arg_type_def |> Dagger.TypeDef.as_list() |> Dagger.ListTypeDef.element_type_def()

      assert {:ok, :STRING_KIND} = Dagger.TypeDef.kind(sub_type_def)

      assert {:ok, "acceptList2"} = Dagger.Function.name(accept_list2)
      assert {:ok, [arg]} = Dagger.Function.args(accept_list2)
      assert {:ok, "alist"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :LIST_KIND} = Dagger.TypeDef.kind(arg_type_def)

      sub_type_def =
        arg_type_def |> Dagger.TypeDef.as_list() |> Dagger.ListTypeDef.element_type_def()

      assert {:ok, :STRING_KIND} = Dagger.TypeDef.kind(sub_type_def)
    end

    test "optional arguments", %{dag: dag} do
      assert {:ok, [optional_arg]} =
               root_object(dag, OptionalArgs) |> Dagger.ObjectTypeDef.functions()

      assert {:ok, "optionalArg"} = Dagger.Function.name(optional_arg)
      assert {:ok, [arg]} = Dagger.Function.args(optional_arg)
      assert {:ok, "s"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :STRING_KIND} = Dagger.TypeDef.kind(arg_type_def)
      assert {:ok, true} = Dagger.TypeDef.optional(arg_type_def)
    end

    test "argument options", %{dag: dag} do
      assert {:ok, [type_option]} =
               root_object(dag, ArgOptions) |> Dagger.ObjectTypeDef.functions()

      assert {:ok, "typeOption"} = Dagger.Function.name(type_option)
      assert {:ok, [arg]} = Dagger.Function.args(type_option)
      assert {:ok, "dir"} = Dagger.FunctionArg.name(arg)
      assert {:ok, "/sdk/elixir"} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, "The directory to run on."} = Dagger.FunctionArg.description(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :OBJECT_KIND} = Dagger.TypeDef.kind(arg_type_def)

      assert {:ok, "Directory"} =
               arg_type_def |> Dagger.TypeDef.as_object() |> Dagger.ObjectTypeDef.name()

      assert {:ok, true} = Dagger.TypeDef.optional(arg_type_def)
    end

    test "return void type", %{dag: dag} do
      assert {:ok, [return_void]} =
               root_object(dag, ReturnVoid) |> Dagger.ObjectTypeDef.functions()

      assert {:ok, "returnVoid"} = Dagger.Function.name(return_void)
      assert {:ok, []} = Dagger.Function.args(return_void)
      return_type_def = Dagger.Function.return_type(return_void)
      assert {:ok, :VOID_KIND} = Dagger.TypeDef.kind(return_type_def)
    end

    test "self object", %{dag: dag} do
      assert {:ok, [only_self_arg, mix_self_and_args]} =
               root_object(dag, SelfObject) |> Dagger.ObjectTypeDef.functions()

      assert {:ok, "onlySelfArg"} = Dagger.Function.name(only_self_arg)
      assert {:ok, []} = Dagger.Function.args(only_self_arg)
      return_type_def = Dagger.Function.return_type(only_self_arg)
      assert {:ok, :VOID_KIND} = Dagger.TypeDef.kind(return_type_def)

      assert {:ok, "mixSelfAndArgs"} = Dagger.Function.name(mix_self_and_args)
      assert {:ok, [arg]} = Dagger.Function.args(mix_self_and_args)
      assert {:ok, "name"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :STRING_KIND} = Dagger.TypeDef.kind(arg_type_def)

      return_type_def = Dagger.Function.return_type(mix_self_and_args)
      assert {:ok, :VOID_KIND} = Dagger.TypeDef.kind(return_type_def)
    end

    test "constructor function", %{dag: dag} do
      root = root_object(dag, ConstructorFunction)

      # No any functions because `init` register as a function.
      assert {:ok, []} = Dagger.ObjectTypeDef.functions(root)

      init = Dagger.ObjectTypeDef.constructor(root)
      assert {:ok, ""} = Dagger.Function.name(init)
      assert {:ok, [arg]} = Dagger.Function.args(init)
      assert {:ok, "name"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :STRING_KIND} = Dagger.TypeDef.kind(arg_type_def)

      return_type_def = Dagger.Function.return_type(init)
      assert {:ok, :OBJECT_KIND} = Dagger.TypeDef.kind(return_type_def)

      assert {:ok, "ConstructorFunction"} =
               return_type_def |> Dagger.TypeDef.as_object() |> Dagger.ObjectTypeDef.name()
    end

    test "accept and return scalar", %{dag: dag} do
      assert {:ok, [accept]} =
               root_object(dag, AcceptAndReturnScalar) |> Dagger.ObjectTypeDef.functions()

      assert {:ok, [arg]} = Dagger.Function.args(accept)
      assert {:ok, "value"} = Dagger.FunctionArg.name(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :SCALAR_KIND} = Dagger.TypeDef.kind(arg_type_def)

      assert {:ok, "Platform"} =
               arg_type_def
               |> Dagger.TypeDef.as_scalar()
               |> Dagger.ScalarTypeDef.name()

      return_type_def = Dagger.Function.return_type(accept)
      assert {:ok, :SCALAR_KIND} = Dagger.TypeDef.kind(return_type_def)

      assert {:ok, "Platform"} =
               return_type_def
               |> Dagger.TypeDef.as_scalar()
               |> Dagger.ScalarTypeDef.name()
    end

    test "accept and return enum", %{dag: dag} do
      assert {:ok, [accept]} =
               root_object(dag, AcceptAndReturnEnum) |> Dagger.ObjectTypeDef.functions()

      assert {:ok, [arg]} = Dagger.Function.args(accept)
      assert {:ok, "value"} = Dagger.FunctionArg.name(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :ENUM_KIND} = Dagger.TypeDef.kind(arg_type_def)

      assert {:ok, "NetworkProtocol"} =
               arg_type_def
               |> Dagger.TypeDef.as_enum()
               |> Dagger.EnumTypeDef.name()

      return_type_def = Dagger.Function.return_type(accept)
      assert {:ok, :ENUM_KIND} = Dagger.TypeDef.kind(return_type_def)

      assert {:ok, "NetworkProtocol"} =
               return_type_def
               |> Dagger.TypeDef.as_enum()
               |> Dagger.EnumTypeDef.name()
    end
  end

  test "deprecated directive", %{dag: dag} do
    root = root_object(dag, DeprecatedDirective)

    assert {:ok, "module deprecation reason"} = Dagger.ObjectTypeDef.deprecated(root)

    assert {:ok, [deprecated_by_attr, deprecated_by_docstr, deprecated_args]} =
             Dagger.ObjectTypeDef.functions(root)

    assert {:ok, "deprecatedByAttr"} = Dagger.Function.name(deprecated_by_attr)

    assert {:ok, "deprecation reason"} =
             Dagger.Function.deprecated(deprecated_by_attr)

    assert {:ok, "deprecatedByDocstr"} = Dagger.Function.name(deprecated_by_docstr)

    assert {:ok, "docstring deprecation reason"} =
             Dagger.Function.deprecated(deprecated_by_docstr)

    assert {:ok, "deprecatedArgs"} = Dagger.Function.name(deprecated_args)
    assert {:ok, [foo, bar]} = Dagger.Function.args(deprecated_args)

    assert {:ok, "deprecated argument"} =
             Dagger.FunctionArg.deprecated(foo)

    assert {:ok, nil} =
             Dagger.FunctionArg.deprecated(bar)

    assert {:ok, [f1, f2]} = Dagger.ObjectTypeDef.fields(root)
    assert {:ok, "f1"} = Dagger.FieldTypeDef.name(f1)
    assert {:ok, "deprecated field"} = Dagger.FieldTypeDef.deprecated(f1)

    assert {:ok, "f2"} = Dagger.FieldTypeDef.name(f2)
    assert {:ok, nil} = Dagger.FieldTypeDef.deprecated(f2)
  end

  defp root_object(dag, module) do
    module = Module.define(dag, module)
    {:ok, [root_object]} = Dagger.Module.objects(module)
    Dagger.TypeDef.as_object(root_object)
  end
end
