defmodule Dagger.EngineConn do
  @moduledoc false

  defstruct [:port, :token]

  @doc """
  Get Dagger engine connection.
  """
  # TODO: plan c: construct conn by downloading cli.
  def get() do
    case from_session_env() do
      {:ok, conn} -> {:ok, conn}
      _otherwise -> from_local_cli()
    end
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

  def from_local_cli() do
    with {:ok, bin} <- System.fetch_env("_EXPERIMENTAL_DAGGER_CLI_BIN"),
         bin when is_binary(bin) <- System.find_executable(bin) do
      logger_pid = start_logger(self())
      _pid = start_cli_session(bin, logger_pid)

      receive do
        {:conn_params, port, token} ->
          {:ok, %__MODULE__{port: port, token: token}}
      after
        300_000 -> {:error, :no_conn_params_receive}
      end
    else
      nil -> {:error, :no_executable}
      otherwise -> otherwise
    end
  end

  defp start_cli_session(bin, caller) do
    spawn_link(fn ->
      port = Port.open({:spawn_executable, bin}, [:binary, :stderr_to_stdout, args: ["session"]])
      session_loop(port, caller)
    end)
  end

  defp session_loop(port, caller) do
    receive do
      {^port, {:data, data}} -> send(caller, {:stdout, data})
    end

    session_loop(port, caller)
  end

  defp start_logger(engine_conn_pid) do
    spawn_link(fn -> logger_loop(engine_conn_pid) end)
  end

  defp logger_loop(engine_conn_pid) do
    receive do
      {:stdout, data} ->
        case Jason.decode(data) do
          {:ok, %{"port" => port, "session_token" => token}} ->
            send(engine_conn_pid, {:conn_params, port, token})

          _otherwise ->
            IO.write(data)
        end
    end

    logger_loop(engine_conn_pid)
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
