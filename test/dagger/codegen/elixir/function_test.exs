defmodule Dagger.Codegen.Elixir.FunctionTest do
  use ExUnit.Case, async: true

  alias Dagger.Codegen.Elixir.Function

  describe "define/5" do
    test "define function" do
      args = [Macro.var(:a, __MODULE__)]

      guard =
        quote do
          is_atom(unquote(Macro.var(:a, __MODULE__)))
        end

      body =
        quote do
          IO.puts(a)
        end

      fun = Function.define(:hello, args, guard, body)

      assert format_code(fun) ==
               """
               @doc false
               def hello(a) when is_atom(a) do
                 IO.puts(a)
               end
               """
               |> format_code()
    end

    test "define function no argument" do
      body =
        quote do
          IO.puts("Hello")
        end

      fun = Function.define(:hello, [], nil, body)

      assert format_code(fun) ==
               """
               @doc false
               def hello() do
                 IO.puts("Hello")
               end
               """
               |> format_code()
    end

    test "define function without guard" do
      args = [Macro.var(:a, __MODULE__)]

      body =
        quote do
          IO.puts(a)
        end

      fun = Function.define(:hello, args, body)

      assert format_code(fun) ==
               """
               @doc false
               def hello(a) do
                 IO.puts(a)
               end
               """
               |> format_code()
    end

    test "define function and have a pattern on argument" do
      args = [
        quote do
          %__MODULE__{} = mod
        end,
        Macro.var(:a, __MODULE__)
      ]

      body =
        quote do
          IO.puts(a)
        end

      fun = Function.define(:hello, args, body)

      assert format_code(fun) ==
               """
               @doc false
               def hello(%__MODULE__{} = mod, a) do
                 IO.puts(a)
               end
               """
               |> format_code()
    end

    test "define doc" do
      args = [
        Macro.var(:a, __MODULE__)
      ]

      body =
        quote do
          IO.puts(a)
        end

      fun = Function.define(:hello, args, nil, body, doc: "Print `a` to stdout")

      assert format_code(fun) ==
               """
               @doc "Print `a` to stdout"
               def hello(a) do
                 IO.puts(a)
               end
               """
               |> format_code()
    end

    test "define deprecated" do
      body =
        quote do
        end

      fun =
        Function.define(:hello, [], nil, body,
          doc: "Print `a` to stdout",
          deprecated: "Use another function"
        )

      assert format_code(fun) ==
               """
               @doc "Print `a` to stdout"
               @deprecated "Use another function"
               def hello() do
               end
               """
               |> format_code()
    end
  end

  defp format_code(code) when is_binary(code) do
    code
    |> Code.string_to_quoted!()
    |> format_code()
  end

  defp format_code(code) do
    code
    |> Code.quoted_to_algebra()
    |> Inspect.Algebra.format(120)
    |> IO.iodata_to_binary()
  end
end
