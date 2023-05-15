defmodule Dagger.EngineConn do
  @moduledoc false

  defstruct [:port, :token, :session_pid]

  @dagger_cli_version "0.5.2"
  @dagger_bin_prefix "dagger-"
  @dagger_default_cli_host "dl.dagger.io"

  @doc false
  def get(opts) do
    case from_session_env(opts) do
      {:ok, conn} ->
        {:ok, conn}

      _otherwise ->
        case from_local_cli(opts) do
          {:ok, conn} -> {:ok, conn}
          _otherwise -> from_remote_cli(opts)
        end
    end
  end

  @doc false
  def from_session_env(_opts) do
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
  def from_local_cli(opts) do
    with {:ok, bin} <- System.fetch_env("_EXPERIMENTAL_DAGGER_CLI_BIN"),
         bin_path when is_binary(bin_path) <- System.find_executable(bin) do
      start_cli_session(bin_path, opts)
    else
      nil -> {:error, :no_executable}
      otherwise -> otherwise
    end
  end

  # https://www.erlang.org/docs/22/man/filename.html

  @doc false
  def from_remote_cli(opts) do
    cache_dir = :filename.basedir(:user_cache, "dagger")
    bin_name = dagger_bin_name(os())
    cache_bin_path = Path.join([cache_dir, bin_name])

    with :ok <- File.mkdir_p(cache_dir),
         :ok <- File.chmod(cache_dir, 0o700),
         {:error, :enoent} <- File.stat(cache_bin_path),
         temp_bin_path = Path.join([cache_dir, "temp-" <> bin_name]),
         :ok <- extract_cli(temp_bin_path),
         :ok <- File.chmod(temp_bin_path, 0o700),
         :ok <- File.rename(temp_bin_path, cache_bin_path) do
      {:ok, cache_bin_path}
    else
      {:ok, _stat} -> {:ok, cache_bin_path}
      error -> error
    end
    |> case do
      {:ok, bin_path} ->
        start_cli_session(bin_path, opts)

      error ->
        error
    end
  end

  # TODO: checksum.
  defp extract_cli(bin_path) do
    with {:ok, response} <- Req.get(cli_archive_url()) do
      {_, dagger_bin} =
        Enum.find(response.body, fn {filename, _bin} ->
          filename == dagger_bin_in_archive(os())
        end)

      File.write(bin_path, dagger_bin)
    end
  end

  defp dagger_bin_in_archive(:windows), do: ~c"dagger.exe"
  defp dagger_bin_in_archive(_), do: ~c"dagger"

  defp cli_archive_url() do
    archive_name = default_cli_archive_name(os(), arch())
    "https://#{@dagger_default_cli_host}/dagger/releases/#{@dagger_cli_version}/#{archive_name}"
  end

  defp arch() do
    case :erlang.system_info(:system_architecture) |> to_string() do
      "aarch64" <> _rest -> "arm64"
      "x86_64" <> _rest -> "amd64"
    end
  end

  defp os() do
    case :os.type() do
      {:unix, :darwin} -> :darwin
      {:unix, :linux} -> :linux
      {:windows, :nt} -> :windows
    end
  end

  defp default_cli_archive_name(os, arch) do
    ext =
      case os do
        os when os in [:linux, :darwin] -> "tar.gz"
        :windows -> "zip"
      end

    "dagger_v#{@dagger_cli_version}_#{os}_#{arch}.#{ext}"
  end

  defp dagger_bin_name(:windows) do
    @dagger_bin_prefix <> @dagger_cli_version <> ".exe"
  end

  defp dagger_bin_name(_) do
    @dagger_bin_prefix <> @dagger_cli_version
  end

  defp start_cli_session(bin_path, opts) do
    connect_timeout = opts[:connect_timeout]
    # TODO: setup logger base on `opts`.
    session_pid =
      spawn_link(Dagger.Session, :start, [bin_path, self(), &Dagger.StdoutLogger.log/1])

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
end
