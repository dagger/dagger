defmodule Dagger.Internal.Engine.Provisioner do
  @moduledoc false

  @dagger_bin_prefix "dagger-"
  @dagger_default_cli_host "dl.dagger.io"

  @doc false
  def provision(cli_version, opts \\ []) do
    cli_host = opts[:cli_host] || @dagger_default_cli_host
    cache_dir = :filename.basedir(:user_cache, "dagger")
    bin_name = dagger_bin_name(cli_version, os())
    cache_bin_path = Path.join([cache_dir, bin_name])
    perm = 0o700

    with :ok <- File.mkdir_p(cache_dir),
         :ok <- File.chmod(cache_dir, perm),
         {:error, :enoent} <- File.stat(cache_bin_path),
         temp_bin_path = Path.join([cache_dir, "temp-" <> bin_name]),
         :ok <- extract_cli(cli_host, cli_version, temp_bin_path),
         :ok <- File.chmod(temp_bin_path, perm),
         :ok <- File.rename(temp_bin_path, cache_bin_path) do
      {:ok, cache_bin_path}
    else
      {:ok, _stat} -> {:ok, cache_bin_path}
      error -> error
    end
  end

  # TODO: checksum.
  defp extract_cli(cli_host, cli_version, bin_path) do
    with {:ok, response} <- Req.get(cli_archive_url(cli_host, cli_version)) do
      {_, dagger_bin} =
        Enum.find(response.body, fn {filename, _bin} ->
          filename == dagger_bin_in_archive(os())
        end)

      File.write(bin_path, dagger_bin)
    end
  end

  defp dagger_bin_in_archive(:windows), do: ~c"dagger.exe"
  defp dagger_bin_in_archive(_), do: ~c"dagger"

  defp cli_archive_url(cli_host, cli_version) do
    archive_name = default_cli_archive_name(os(), arch(), cli_version)
    "https://#{cli_host}/dagger/releases/#{cli_version}/#{archive_name}"
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

  defp default_cli_archive_name(os, arch, cli_version) do
    ext =
      case os do
        os when os in [:linux, :darwin] -> "tar.gz"
        :windows -> "zip"
      end

    "dagger_v#{cli_version}_#{os}_#{arch}.#{ext}"
  end

  defp dagger_bin_name(cli_version, :windows) do
    @dagger_bin_prefix <> cli_version <> ".exe"
  end

  defp dagger_bin_name(cli_version, _) do
    @dagger_bin_prefix <> cli_version
  end
end
