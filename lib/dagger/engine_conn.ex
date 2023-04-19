defmodule Dagger.EngineConn do
  @moduledoc false

  defstruct [:port, :token]

  @doc """
  Get Dagger engine connection.
  """
  # TODO: handle error.
  # TODO: plan b: construct conn by using local cli.
  # TODO: plan c: construct conn by downloading cli.
  def get() do
    from_session_env()
  end

  @doc """
  Getting Dagger connection from environment variables.
  """
  def from_session_env() do
    with {:ok, port} <- System.fetch_env("DAGGER_SESSION_PORT"),
         {:ok, token} <- System.fetch_env("DAGGER_SESSION_TOKEN") do
      {:ok,
       %__MODULE__{
         port: port,
         token: token
       }}
    end
  end

  @doc """
  Constructing host connection.
  """
  def host(%__MODULE__{port: port}), do: "127.0.0.1:#{port}"

  @doc """
  Get the token.
  """
  def token(%__MODULE__{token: token}), do: token
end
