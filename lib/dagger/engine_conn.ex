defmodule Dagger.EngineConn do
  @moduledoc false

  defstruct [:port, :token, :session_pid]

  @doc false
  # TODO: plan c: construct conn by downloading cli.
  def get() do
    case from_session_env() do
      {:ok, conn} -> {:ok, conn}
      _otherwise -> from_local_cli()
    end
  end

  @doc false
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

  @doc false
  def from_local_cli() do
    with {:ok, bin} <- System.fetch_env("_EXPERIMENTAL_DAGGER_CLI_BIN"),
         bin when is_binary(bin) <- System.find_executable(bin) do
      session_pid = spawn_link(Dagger.Session, :start, [bin, self(), &Dagger.StdoutLogger.log/1])

      receive do
        {^session_pid, %{"port" => port, "session_token" => token}} ->
          {:ok, %__MODULE__{port: port, token: token, session_pid: session_pid}}
      after
        300_000 -> {:error, :session_timeout}
      end
    else
      nil -> {:error, :no_executable}
      otherwise -> otherwise
    end
  end

  # Constructing host connection.
  @doc false
  def host(%__MODULE__{port: port}), do: "127.0.0.1:#{port}"

  # Get the token.
  @doc false
  def token(%__MODULE__{token: token}), do: token

  # Disconnecting from Dagger session.
  @doc false
  def disconnect(%__MODULE__{session_pid: nil}) do
    :ok
  end

  def disconnect(%__MODULE__{session_pid: pid}) do
    Dagger.Session.stop(pid)
  end
end
