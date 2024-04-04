defmodule Dagger.Core.EngineConn do
  @moduledoc false

  alias Dagger.Core.Engine.Downloader

  defstruct [:port, :token, :session_pid]

  @dagger_cli_version "0.11.0"

  @doc false
  def get(opts) do
    case from_session_env(opts) do
      {:ok, conn} ->
        {:ok, conn}

      {:error, :workdir_configure_on_session} = error ->
        error

      _otherwise ->
        case from_local_cli(opts) do
          {:ok, conn} -> {:ok, conn}
          {:error, :no_executable} -> from_remote_cli(opts)
          otherwise -> otherwise
        end
    end
  end

  @doc false
  def from_session_env(opts) do
    with {:ok, port} <- System.fetch_env("DAGGER_SESSION_PORT"),
         {:ok, token} <- System.fetch_env("DAGGER_SESSION_TOKEN"),
         false <- Keyword.has_key?(opts, :workdir) do
      {:ok,
       %__MODULE__{
         port: port,
         token: token
       }}
    else
      true -> {:error, :workdir_configure_on_session}
      :error -> {:error, :no_session}
    end
  end

  @doc false
  def from_local_cli(opts) do
    with {:ok, bin} <- System.fetch_env("_EXPERIMENTAL_DAGGER_CLI_BIN"),
         bin = Path.expand(bin),
         bin_path when is_binary(bin_path) <- System.find_executable(bin) do
      start_cli_session(bin_path, opts)
    else
      :error -> {:error, :no_executable}
      nil -> {:error, :no_executable}
      otherwise -> otherwise
    end
  end

  @doc false
  def from_remote_cli(opts) do
    case Downloader.download(@dagger_cli_version) do
      {:ok, bin_path} ->
        start_cli_session(bin_path, opts)

      error ->
        error
    end
  end

  defp start_cli_session(bin_path, opts) do
    connect_timeout = opts[:connect_timeout]
    session_pid = spawn_link(Dagger.Session, :start, [bin_path, self(), opts])

    receive do
      {^session_pid, %{"port" => port, "session_token" => token}} ->
        {:ok, %__MODULE__{port: port, token: token, session_pid: session_pid}}
    after
      connect_timeout -> {:error, :session_timeout}
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

  def engine_version(), do: @dagger_cli_version
end
