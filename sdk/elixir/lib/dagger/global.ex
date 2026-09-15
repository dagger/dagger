defmodule Dagger.Global do
  @moduledoc false

  use GenServer

  @doc """
  Starting the process and connecting it to the Dagger.
  """
  def start_link(_opts \\ []) do
    GenServer.start_link(__MODULE__, [], name: __MODULE__)
  end

  @doc """
  Get the Dagger client.
  """
  def dag() do
    GenServer.call(__MODULE__, :dag)
  end

  @doc """
  Close the Dagger client.
  """
  def close() do
    GenServer.stop(__MODULE__, :normal)
  end

  @impl GenServer
  def init([]) do
    Dagger.connect()
  end

  @impl GenServer
  def handle_call(:dag, _from, dag), do: {:reply, dag, dag}

  @impl GenServer
  def terminate(_reason, dag) do
    Dagger.close(dag)
  end
end
