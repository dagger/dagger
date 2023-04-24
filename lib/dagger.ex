defmodule Dagger do
  @moduledoc """
  Documentation for `Dagger`.
  """

  use Dagger.QueryBuilder

  defstruct [:client, :query]

  def connect(opts \\ []) do
    with {:ok, client} <- Dagger.Client.connect(opts) do
      {:ok,
       %Dagger.Query{
         client: client,
         selection: query()
       }}
    end
  end

  def connect!(opts \\ []) do
    case connect(opts) do
      {:ok, query} -> query
      error -> raise "Cannot connect to Dagger engine, cause: #{inspect(error)}"
    end
  end

  def disconnect(%Dagger.Query{client: client}) do
    Dagger.Client.disconnect(client)
  end
end
