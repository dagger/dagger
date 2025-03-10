# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.EnvVariable do
  @moduledoc "An environment variable name and value."

  alias Dagger.Core.Client
  alias Dagger.Core.QueryBuilder, as: QB

  @derive Dagger.ID

  defstruct [:query_builder, :client]

  @type t() :: %__MODULE__{}

  @doc "A unique identifier for this EnvVariable."
  @spec id(t()) :: {:ok, Dagger.EnvVariableID.t()} | {:error, term()}
  def id(%__MODULE__{} = env_variable) do
    query_builder =
      env_variable.query_builder |> QB.select("id")

    Client.execute(env_variable.client, query_builder)
  end

  @doc "The environment variable name."
  @spec name(t()) :: {:ok, String.t()} | {:error, term()}
  def name(%__MODULE__{} = env_variable) do
    query_builder =
      env_variable.query_builder |> QB.select("name")

    Client.execute(env_variable.client, query_builder)
  end

  @doc "The environment variable value."
  @spec value(t()) :: {:ok, String.t()} | {:error, term()}
  def value(%__MODULE__{} = env_variable) do
    query_builder =
      env_variable.query_builder |> QB.select("value")

    Client.execute(env_variable.client, query_builder)
  end
end

defimpl Jason.Encoder, for: Dagger.EnvVariable do
  def encode(env_variable, opts) do
    {:ok, id} = Dagger.EnvVariable.id(env_variable)
    Jason.Encode.string(id, opts)
  end
end

defimpl Nestru.Decoder, for: Dagger.EnvVariable do
  def decode_fields_hint(_struct, _context, id) do
    {:ok, Dagger.Client.load_env_variable_from_id(Dagger.Global.dag(), id)}
  end
end
