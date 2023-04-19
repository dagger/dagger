defmodule Dagger.EngineConn do
  @moduledoc false

  defstruct [:port, :token]

  @doc """
  Get Dagger engine connection.
  """
  # TODO: plan b: construct conn by using local cli.
  # TODO: plan c: construct conn by downloading cli.
  def get() do
    from_session_env()
  end

  def from_session_env() do
    port = System.get_env("DAGGER_SESSION_PORT")
    token = System.get_env("DAGGER_SESSION_TOKEN")

    {:ok,
     %__MODULE__{
       port: port,
       token: token
     }}
  end

  def host(%__MODULE__{port: port}), do: "127.0.0.1:#{port}"

  def token(%__MODULE__{token: token}), do: token
end
