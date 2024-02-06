defmodule Dagger.Core.QueryBuilder.Selection do
  @moduledoc false

  defstruct [:name, :args, :prev, alias: ""]

  def query(), do: %__MODULE__{}

  def select(%__MODULE__{} = selection, name) when is_binary(name) do
    select_with_alias(selection, "", name)
  end

  def select_with_alias(%__MODULE__{} = selection, alias, name)
      when is_binary(alias) and is_binary(name) do
    %__MODULE__{
      name: name,
      alias: alias,
      prev: selection
    }
  end

  def arg(%__MODULE__{args: args} = selection, name, value) when is_binary(name) do
    args = args || %{}

    %{selection | args: Map.put(args, name, value)}
  end

  def build(%__MODULE__{} = selection) do
    fields = build_fields(selection, [])
    Enum.join(fields, "{") <> String.duplicate("}", Enum.count(fields) - 1)
  end

  def build_fields(%__MODULE__{prev: nil}, acc) do
    ["query" | acc]
  end

  def build_fields(%__MODULE__{prev: selection, name: name, args: args, alias: alias}, acc) do
    q = [build_alias(alias) | [name | build_args(args)]]
    build_fields(selection, [IO.iodata_to_binary(q) | acc])
  end

  defp build_alias(""), do: []
  defp build_alias(alias), do: [alias, ~c":"]

  defp build_args(nil), do: []

  defp build_args(args) do
    fun = fn {name, value} -> [name, ~c":", encode_value(value)] end
    [~c"(", Enum.map(args, fun) |> Enum.intersperse(","), ~c")"]
  end

  defp encode_value(value) when is_atom(value) and not is_boolean(value),
    do: encode_value(to_string(value))

  defp encode_value(value) when is_binary(value) do
    string =
      value
      |> String.replace("\n", "\\n")
      |> String.replace("\t", "\\t")
      |> String.replace("\"", "\\\"")

    [~c"\"", string, ~c"\""]
  end

  defp encode_value(value) when is_list(value) do
    [~c"[", Enum.map_intersperse(value, ",", &encode_value/1), ~c"]"]
  end

  defp encode_value(value) when is_struct(value) do
    value
    |> Map.from_struct()
    |> encode_value()
  end

  defp encode_value(value) when is_map(value) do
    fun = fn {name, value} ->
      [to_string(name), ~c":", encode_value(value)]
    end

    [~c"{", Enum.map_intersperse(value, ",", fun), ~c"}"]
  end

  defp encode_value(value), do: [to_string(value)]

  def path(selection) do
    path(selection, [])
  end

  def path(%__MODULE__{prev: nil, name: nil}, acc), do: acc
  def path(%__MODULE__{prev: selection, name: name}, acc), do: path(selection, [name | acc])
end

defmodule Dagger.QueryError do
  @moduledoc false

  defstruct [:errors]
end

defmodule Dagger.Core.QueryBuilder do
  @moduledoc false

  alias Dagger.Core.QueryBuilder.Selection
  alias Dagger.Core.Client

  def execute(selection, client) do
    q = Selection.build(selection)

    case Client.query(client, q) do
      {:ok, %{status: 200, body: %{"data" => nil, "errors" => errors}}} ->
        {:error, %Dagger.QueryError{errors: errors}}

      {:ok, %{status: 200, body: %{"data" => data}}} ->
        {:ok, select_data(data, Selection.path(selection) |> Enum.reverse())}

      otherwise ->
        otherwise
    end
  end

  defp select_data(data, [sub_selection | path]) do
    case sub_selection |> String.split() do
      [selection] ->
        get_in(data, Enum.reverse([selection | path]))

      selections ->
        case get_in(data, Enum.reverse(path)) do
          data when is_list(data) -> Enum.map(data, &Map.take(&1, selections))
          data when is_map(data) -> Map.take(data, selections)
        end
    end
  end

  defmacro __using__(_opts) do
    quote do
      import Dagger.Core.QueryBuilder.Selection
      import Dagger.Core.QueryBuilder, only: [execute: 2]
    end
  end
end
