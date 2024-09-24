defmodule Dagger.Mod.Object.Defn do
  @moduledoc false

  @doc false
  def define(name, args, return, block) do
    typespec_args =
      case args do
        {_self, args} ->
          [quote(do: t()) | Enum.map(args, &typespec_arg/1)]

        args ->
          Enum.map(args, &typespec_arg/1)
      end

    args =
      case args do
        {self, args} ->
          [var(self) | Enum.map(args, &var/1)]

        args ->
          Enum.map(args, &var/1)
      end

    quote do
      @spec unquote(name)(unquote_splicing(typespec_args)) :: unquote(return)
      def unquote(name)(unquote_splicing(args)) do
        unquote(block)
      end
    end
  end

  # {var, type}
  defp var({name, _}) do
    Macro.var(name, nil)
  end

  # var
  defp var({name, _, nil}) do
    Macro.var(name, nil)
  end

  # pattern = var
  defp var({:=, _, [_, v]}) do
    var(v)
  end

  defp typespec_arg({name, {type, _}}) do
    typespec_arg({name, type})
  end

  defp typespec_arg({_, type} = arg) do
    arg = var(arg)

    quote do
      unquote(arg) :: unquote(type)
    end
  end
end
