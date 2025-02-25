# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.Engine do
  @moduledoc "The Dagger engine configuration and state"

  alias Dagger.Core.Client
  alias Dagger.Core.QueryBuilder, as: QB

  @derive Dagger.ID

  defstruct [:query_builder, :client]

  @type t() :: %__MODULE__{}

  @doc "A unique identifier for this Engine."
  @spec id(t()) :: {:ok, Dagger.EngineID.t()} | {:error, term()}
  def id(%__MODULE__{} = engine) do
    query_builder =
      engine.query_builder |> QB.select("id")

    Client.execute(engine.client, query_builder)
  end

  @doc "The local (on-disk) cache for the Dagger engine"
  @spec local_cache(t()) :: Dagger.EngineCache.t()
  def local_cache(%__MODULE__{} = engine) do
    query_builder =
      engine.query_builder |> QB.select("localCache")

    %Dagger.EngineCache{
      query_builder: query_builder,
      client: engine.client
    }
  end
end

defimpl Jason.Encoder, for: Dagger.Engine do
  def encode(engine, opts) do
    {:ok, id} = Dagger.Engine.id(engine)
    Jason.Encode.string(id, opts)
  end
end

defimpl Nestru.Decoder, for: Dagger.Engine do
  def decode_fields_hint(_struct, _context, id) do
    {:ok, Dagger.Client.load_engine_from_id(Dagger.Global.dag(), id)}
  end
end
