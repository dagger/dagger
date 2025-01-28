# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.SpanContext do
  @moduledoc "Dagger.SpanContext"

  alias Dagger.Core.Client
  alias Dagger.Core.QueryBuilder, as: QB

  @derive Dagger.ID

  defstruct [:query_builder, :client]

  @type t() :: %__MODULE__{}

  @doc "A unique identifier for this SpanContext."
  @spec id(t()) :: {:ok, Dagger.SpanContextID.t()} | {:error, term()}
  def id(%__MODULE__{} = span_context) do
    query_builder =
      span_context.query_builder |> QB.select("id")

    Client.execute(span_context.client, query_builder)
  end

  @spec remote(t()) :: {:ok, boolean()} | {:error, term()}
  def remote(%__MODULE__{} = span_context) do
    query_builder =
      span_context.query_builder |> QB.select("remote")

    Client.execute(span_context.client, query_builder)
  end

  @spec span_id(t()) :: {:ok, String.t()} | {:error, term()}
  def span_id(%__MODULE__{} = span_context) do
    query_builder =
      span_context.query_builder |> QB.select("spanId")

    Client.execute(span_context.client, query_builder)
  end

  @spec trace_id(t()) :: {:ok, String.t()} | {:error, term()}
  def trace_id(%__MODULE__{} = span_context) do
    query_builder =
      span_context.query_builder |> QB.select("traceId")

    Client.execute(span_context.client, query_builder)
  end
end
