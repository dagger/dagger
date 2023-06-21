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
      |> Selection.arg("arg", ["value"])

    assert Selection.build(root) == "query{a(arg:[\"value\"])}"
  end
end
