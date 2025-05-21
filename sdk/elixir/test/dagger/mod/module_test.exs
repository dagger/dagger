defmodule Dagger.Mod.ModuleTest do
  use ExUnit.Case, async: true

  alias Dagger.Mod.Module

  setup_all do
    dag = Dagger.connect!(connect_timeout: :timer.seconds(60))
    on_exit(fn -> Dagger.close(dag) end)

    %{dag: dag}
  end

  describe "define/1" do
    test "register module", %{dag: dag} do
      module = Module.define(dag, ObjectMod)

      assert {:ok, [root_module]} = Dagger.Module.objects(module)

      assert {:ok, functions} =
               root_module
               |> Dagger.TypeDef.as_object()
               |> Dagger.ObjectTypeDef.functions()

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

      [accept_boolean | functions] = functions
      assert {:ok, "acceptBoolean"} = Dagger.Function.name(accept_boolean)
      assert {:ok, [arg]} = Dagger.Function.args(accept_boolean)
      assert {:ok, "name"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      assert {:ok, :BOOLEAN_KIND} = Dagger.FunctionArg.type_def(arg) |> Dagger.TypeDef.kind()

      [empty_args | functions] = functions
      assert {:ok, "emptyArgs"} = Dagger.Function.name(empty_args)
      assert {:ok, []} = Dagger.Function.args(empty_args)

      [accept_and_return_module | functions] = functions
      assert {:ok, "acceptAndReturnModule"} = Dagger.Function.name(accept_and_return_module)
      assert {:ok, [arg]} = Dagger.Function.args(accept_and_return_module)
      assert {:ok, "container"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :OBJECT_KIND} = Dagger.TypeDef.kind(arg_type_def)

      assert {:ok, "Container"} =
               arg_type_def |> Dagger.TypeDef.as_object() |> Dagger.ObjectTypeDef.name()

      [accept_list | functions] = functions
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

      [accept_list2 | functions] = functions
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

      [optional_arg | functions] = functions
      assert {:ok, "optionalArg"} = Dagger.Function.name(optional_arg)
      assert {:ok, [arg]} = Dagger.Function.args(optional_arg)
      assert {:ok, "s"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :STRING_KIND} = Dagger.TypeDef.kind(arg_type_def)
      assert {:ok, true} = Dagger.TypeDef.optional(arg_type_def)

      [type_option | functions] = functions
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

      [return_void | functions] = functions
      assert {:ok, "returnVoid"} = Dagger.Function.name(return_void)
      assert {:ok, []} = Dagger.Function.args(return_void)
      return_type_def = Dagger.Function.return_type(return_void)
      assert {:ok, :VOID_KIND} = Dagger.TypeDef.kind(return_type_def)

      [only_self_arg | functions] = functions
      assert {:ok, "onlySelfArg"} = Dagger.Function.name(only_self_arg)
      assert {:ok, []} = Dagger.Function.args(only_self_arg)
      return_type_def = Dagger.Function.return_type(return_void)
      assert {:ok, :VOID_KIND} = Dagger.TypeDef.kind(return_type_def)

      [mix_self_and_args | _functions] = functions
      assert {:ok, "mixSelfAndArgs"} = Dagger.Function.name(mix_self_and_args)
      assert {:ok, [arg]} = Dagger.Function.args(mix_self_and_args)
      assert {:ok, "name"} = Dagger.FunctionArg.name(arg)
      assert {:ok, ""} = Dagger.FunctionArg.default_path(arg)
      assert {:ok, nil} = Dagger.FunctionArg.default_value(arg)
      arg_type_def = Dagger.FunctionArg.type_def(arg)
      assert {:ok, :STRING_KIND} = Dagger.TypeDef.kind(arg_type_def)

      return_type_def = Dagger.Function.return_type(return_void)
      assert {:ok, :VOID_KIND} = Dagger.TypeDef.kind(return_type_def)
    end
  end
end
