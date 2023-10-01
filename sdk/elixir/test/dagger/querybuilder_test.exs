defmodule Dagger.QueryBuilder.SelectionTest do
  use ExUnit.Case, async: true

  alias Dagger.QueryBuilder.Selection

  test "query" do
    root =
      Selection.query()
      |> Selection.select("core")
      |> Selection.select("image")
      |> Selection.arg("ref", "alpine")
      |> Selection.select("file")
      |> Selection.arg("path", "/etc/alpine-release")

    assert Selection.build(root) ==
             "query{core{image(ref:\"alpine\"){file(path:\"/etc/alpine-release\")}}}"
  end

  test "alias" do
    root =
      Selection.query()
      |> Selection.select("core")
      |> Selection.select("image")
      |> Selection.arg("ref", "alpine")
      |> Selection.select_with_alias("foo", "file")
      |> Selection.arg("path", "/etc/alpine-release")

    assert Selection.build(root) ==
             "query{core{image(ref:\"alpine\"){foo:file(path:\"/etc/alpine-release\")}}}"
  end

  test "select multi fields" do
    root =
      Selection.query()
      |> Selection.select("core")
      |> Selection.select("name value")

    assert Selection.build(root) == "query{core{name value}}"
  end

  test "args" do
    root =
      Selection.query()
      |> Selection.select("a")
      |> Selection.arg("arg", "b")
      |> Selection.arg("arg1", "c")

    assert Selection.build(root) == "query{a(arg:\"b\",arg1:\"c\")}"
  end

  test "arg collision" do
    root =
      Selection.query()
      |> Selection.select("a")
      |> Selection.arg("arg", "one")
      |> Selection.select("b")
      |> Selection.arg("arg", "two")

    assert Selection.build(root) == "query{a(arg:\"one\"){b(arg:\"two\")}}"
  end

  test "array args" do
    root =
      Selection.query()
      |> Selection.select("a")
      |> Selection.arg("arg", [])

    assert Selection.build(root) == "query{a(arg:[])}"

    root =
      Selection.query()
      |> Selection.select("a")
      |> Selection.arg("arg", ["value"])

    assert Selection.build(root) == "query{a(arg:[\"value\"])}"

    root =
      Selection.query()
      |> Selection.select("a")
      |> Selection.arg("arg", ["value", "value2"])

    assert Selection.build(root) == "query{a(arg:[\"value\",\"value2\"])}"

    root =
      Selection.query()
      |> Selection.select("a")
      |> Selection.arg("arg", [%{"name" => "foo"}])

    assert Selection.build(root) == "query{a(arg:[{name:\"foo\"}])}"
  end

  test "object args" do
    root =
      Selection.query()
      |> Selection.select("a")
      |> Selection.arg("arg", %{"name" => "a", "value" => "b"})

    assert Selection.build(root) == "query{a(arg:{name:\"a\",value:\"b\"})}"
  end

  test "string arg escape" do
    root =
      Selection.query()
      |> Selection.select("a")
      |> Selection.arg("arg", "\n\t\"")

    assert Selection.build(root) == "query{a(arg:\"\\n\\t\\\"\")}"
  end

  test "boolean arg" do
    root =
      Selection.query()
      |> Selection.select("a")
      |> Selection.arg("arg", true)

    assert Selection.build(root) == "query{a(arg:true)}"
  end
end
