defmodule Dagger.Mod.Object.Meta do
  @moduledoc false

  def from_options(options) when is_list(options) do
  end

  def validate!(meta) do
    meta = Keyword.validate!(meta, [:type, doc: nil, default_path: nil, ignore: nil])
    :ok = Enum.each(meta, &validate/1)

    meta
  end

  defp validate({:type, type}) when is_atom(type) or is_tuple(type), do: :ok
  defp validate({:doc, doc}) when is_binary(doc) or is_nil(doc), do: :ok
  defp validate({:default_path, path}) when is_binary(path) or is_nil(path), do: :ok
  defp validate({:ignore, patterns}) when is_list(patterns) or is_nil(patterns), do: :ok
end
