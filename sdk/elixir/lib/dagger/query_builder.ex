defmodule Dagger.QueryBuilder.Selection do
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
  defp build_alias(alias), do: [alias, ':']

  defp build_args(nil), do: []

  defp build_args(args) do
    fun = fn {name, value} -> [name, ':', Jason.encode!(value)] end
    ['(', Enum.map_join(args, ",", fun), ')']
  end

  def path(selection) do
    path(selection, [])
  end

  def path(%__MODULE__{prev: nil, name: nil}, acc), do: acc
  def path(%__MODULE__{prev: selection, name: name}, acc), do: path(selection, [name | acc])
end

defmodule Dagger.QueryError do
  @moduledoc false

  # TODO: use defexception.

  defstruct [:errors]
end

defmodule Dagger.QueryBuilder do
  @moduledoc false

  alias Dagger.QueryBuilder.Selection
  alias Dagger.Internal.Client

  def execute(selection, client) do
    q = Selection.build(selection)

    case Client.query(client, q) do
      {:ok, %{status: 200, body: %{"data" => nil, "errors" => errors}}} ->
        {:error, %Dagger.QueryError{errors: errors}}

      {:ok, %{status: 200, body: %{"data" => data}}} ->
        # TODO: returns {:ok, response}.
        select_data(data, Selection.path(selection) |> Enum.reverse())

      otherwise ->
        otherwise
    end
  end

  defp select_data(data, [leaf | path]) do
    case leaf |> String.split() do
      children when is_list(children) ->
        case get_in(data, Enum.reverse(path)) do
          data when is_list(data) -> Enum.map(data, &Map.take(&1, children))
          data when is_map(data) -> Map.take(data, children)
        end

      children when is_binary(children) ->
        get_in(data, Enum.reverse([children | path]))
    end
  end

  defmacro __using__(_opts) do
    quote do
      import Dagger.QueryBuilder.Selection
      import Dagger.QueryBuilder, only: [execute: 2]
    end
  end
end
