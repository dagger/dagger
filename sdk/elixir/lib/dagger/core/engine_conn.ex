defmodule Dagger.Core.EngineConn do
  @moduledoc false

  alias Dagger.Core.Engine.Downloader

  defstruct [:port, :token, :session_pid]

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
    from_remote_cli(opts, Downloader, &start_cli_session/2)
  end

  @doc false
  def from_remote_cli(opts, downloader) do
    from_remote_cli(opts, downloader, &start_cli_session/2)
  end

  @doc false
  def from_remote_cli(opts, downloader, start_session) do
    cli_version = Dagger.Core.Version.engine_version()

    case downloader.download(cli_version) do
      {:ok, bin_path} ->
        start_session.(bin_path, opts)

      {:error, {:cli_release_unavailable, _} = download_error} ->
        with {:ok, bin_path} <-
               fallback_to_local_cli(download_error, cli_version, opts[:log_output] || :stderr) do
          case start_session.(bin_path, opts) do
            {:ok, _conn} = result ->
              result

            session_error ->
              {:error,
               {:cli_fallback_failed, download_error,
                {:failed_to_use_cli_from_path, bin_path, session_error}}}
          end
        end

      error ->
        error
    end
  end

  @doc false
  def fallback_to_local_cli(download_error, cli_version, log_output \\ :stderr)

  def fallback_to_local_cli(
        {:cli_release_unavailable, _} = download_error,
        cli_version,
        log_output
      ) do
    case System.find_executable("dagger") do
      bin_path when is_binary(bin_path) ->
        IO.puts(
          log_output,
          "CLI version #{cli_version} is unavailable; using #{bin_path} from PATH " <>
            "(version compatibility is not guaranteed)."
        )

        {:ok, bin_path}

      nil ->
        path_error =
          {:dagger_cli_not_found, "dagger CLI not found in PATH", :no_executable}

        {:error, {:cli_fallback_failed, download_error, path_error}}
    end
  end

  def fallback_to_local_cli(download_error, _cli_version, _log_output) do
    {:error, download_error}
  end

  defp start_cli_session(bin_path, opts) do
    connect_timeout = opts[:connect_timeout]

    {session_pid, monitor_ref} =
      spawn_monitor(Dagger.Session, :start, [bin_path, self(), opts])

    receive do
      {^session_pid, %{"port" => port, "session_token" => token}} ->
        Process.link(session_pid)
        Process.demonitor(monitor_ref, [:flush])
        {:ok, %__MODULE__{port: port, token: token, session_pid: session_pid}}

      {:DOWN, ^monitor_ref, :process, ^session_pid, reason} ->
        {:error, {:session_start_failed, reason}}
    after
      connect_timeout ->
        Process.exit(session_pid, :kill)
        Process.demonitor(monitor_ref, [:flush])
        {:error, :session_timeout}
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

  def engine_version() do
    Dagger.Core.Version.engine_version()
  end
end
