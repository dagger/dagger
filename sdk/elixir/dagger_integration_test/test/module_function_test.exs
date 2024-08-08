defmodule Dagger.ModuleFunctionTest do
  use Dagger.Case, async: true

  import Dagger.TestHelper

  test "signatures", %{dag: dag} do
    mod =
      dag
      |> dagger_cli_base()
      |> dagger_init()
      |> dagger_with_source("test/lib/test.ex", """
      defmodule Test do
        use Dagger.Mod.Object, name: "Test"

        function [], String.t()
        def hello(_args), do: "hello"

        function [msg: String.t()], String.t()
        def echo(args), do: args.msg

        function [msg: list(String.t())], String.t()
        def echo_list(args), do: Enum.join(args.msg, "+")

        function [msg: [String.t()]], String.t()
        def echo_list2(args), do: Enum.join(args.msg, "+")
      end
      """)

    assert query(mod, """
           {
             test {
               hello
             }
           }
           """) ==
             """
             {
                 "test": {
                     "hello": "hello"
                 }
             }
             """

    assert query(mod, """
           {
             test {
               echo(msg: "world")
             }
           }
           """) == """
           {
               "test": {
                   "echo": "world"
               }
           }
           """

    assert query(mod, """
           {
             test {
               echoList(msg: ["a", "b", "c"])
             }
           }
           """) == """
           {
               "test": {
                   "echoList": "a+b+c"
               }
           }
           """

    assert query(mod, """
           {
             test {
               echoList2(msg: ["a", "b", "c"])
             }
           }
           """) == """
           {
               "test": {
                   "echoList2": "a+b+c"
               }
           }
           """
  end

  test "signatures builtin types", %{dag: dag} do
    mod =
      dag
      |> dagger_cli_base()
      |> dagger_init()
      |> dagger_with_source("test/lib/test.ex", """
      defmodule Test do
        use Dagger.Mod.Object, name: "Test"

        function [dir: Dagger.Directory.t()], String.t()
        def read(args) do
          args.dir
          |> Dagger.Directory.file( "foo")
          |> Dagger.File.contents()
        end
      end
      """)

    assert {:ok, dir_id} =
             dag
             |> Dagger.Client.directory()
             |> Dagger.Directory.with_new_file("foo", "bar")
             |> Dagger.Directory.id()

    assert query(mod, """
           {
             test {
               read(dir: "#{dir_id}")
             }
           }
           """) == """
           {
               "test": {
                   "read": "bar"
               }
           }
           """
  end

  defp query(mod, q) do
    assert {:ok, output} =
             mod
             |> dagger_query(q)
             |> stdout()

    output
  end
end
