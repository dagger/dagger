defmodule Dagger.Core.Base do
  @moduledoc false

  defmacro __using__(opts) when is_list(opts) do
    kind = Keyword.fetch!(opts, :kind)
    name = Keyword.fetch!(opts, :name)

    quote do
      def __kind__(), do: unquote(kind)
      def __name__(), do: unquote(name)
    end
  end
end
