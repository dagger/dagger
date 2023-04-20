defmodule Dagger do
  @moduledoc """
  Documentation for `Dagger`.
  """

  use Dagger.QueryBuilder

  defstruct [:client, :query]

  def connect() do
    with {:ok, client} <- Dagger.Client.connect() do
      {:ok,
       %Dagger.Query{
         client: client,
         selection: query()
       }}
    end
  end

  def connect!() do
    case connect() do
      {:ok, query} -> query
      error -> raise "Cannot connect to Dagger engine, cause: #{inspect(error)}"
    end
  end
end
