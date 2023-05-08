defmodule Dagger do
  @moduledoc """
  The [Dagger](https://dagger.io/) SDK for Elixir.

  See `getting_start.livemd` for starter point.
  """

  use Dagger.QueryBuilder

  defstruct [:client, :query]

  @connect_schema [
    workdir: [
      type: :string,
      doc: "Sets the engine workdir."
    ],
    config_path: [
      type: :string,
      doc: "Sets the engine config path."
    ],
    log_output: [
      type: :atom,
      doc: "Sets the progress writer."
    ],
    connect_timeout: [
      type: :timeout,
      doc: "Sets timeout when connect to the engine.",
      default: :timer.seconds(10)
    ],
    query_timeout: [
      type: :timeout,
      doc: "Sets timeout when executing a query.",
      default: :infinity
    ]
  ]

  @doc """
  Connecting to Dagger.

  ## Options

  #{NimbleOptions.docs(@connect_schema)}
  """
  def connect(opts \\ []) do
    with {:ok, opts} <- NimbleOptions.validate(opts, @connect_schema),
         {:ok, client} <- Dagger.Client.connect(opts) do
      {:ok,
       %Dagger.Query{
         client: client,
         selection: query()
       }}
    end
  end

  @doc """
  Similar to `connect/1` but raise exception when found an error.
  """
  def connect!(opts \\ []) do
    case connect(opts) do
      {:ok, query} -> query
      error -> raise "Cannot connect to Dagger engine, cause: #{inspect(error)}"
    end
  end

  @doc """
  Disconnecting Dagger.
  """
  def disconnect(%Dagger.Query{client: client}) do
    Dagger.Client.disconnect(client)
  end
end
