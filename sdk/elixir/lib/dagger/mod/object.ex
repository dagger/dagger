defmodule Dagger.Mod.Object do
  @moduledoc """
  A module for declare an object type.
  """

  @doc """
  Declare a function spec.
  """
  defmacro function(args, return) do
    args = compile_args(args)
    return = compile_typespec!(return)

    quote do
      @function [args: unquote(args), return: unquote(return)]
    end
  end

  defmacro __using__(opts) do
    quote do
      use Dagger.Mod, unquote(opts)

      import Dagger.Mod.Object, only: [function: 2]
      import Dagger.Global, only: [dag: 0]

      @on_definition Dagger.Mod
      @functions []

      Module.register_attribute(__MODULE__, :functions, persist: true)
    end
  end

  defp compile_args(args) do
    for {name, spec} <- args do
      {name, [type: compile_typespec!(spec)]}
    end
  end

  # binary()
  defp compile_typespec!({:binary, _, []}), do: :string
  # integer()
  defp compile_typespec!({:integer, _, []}), do: :integer
  # boolean()
  defp compile_typespec!({:boolean, _, []}), do: :boolean

  # String.t() 
  defp compile_typespec!(
         {{:., _,
           [
             {:__aliases__, _, [:String]},
             :t
           ]}, _, []}
       ) do
    :string
  end

  defp compile_typespec!({{:., _, [{:__aliases__, _, module}, :t]}, _, []}) do
    Module.concat(module)
  end

  defp compile_typespec!({:list, _, [type]}) do
    {:list, compile_typespec!(type)}
  end

  defp compile_typespec!([type]) do
    {:list, compile_typespec!(type)}
  end

  defp compile_typespec!(unsupported_type) do
    raise ArgumentError, "type `#{Macro.to_string(unsupported_type)}` is not supported"
  end
end
