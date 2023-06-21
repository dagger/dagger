defmodule Dagger.Internal.Engine.Downloader do
  @moduledoc false

  @dagger_bin_prefix "dagger-"
  @dagger_default_cli_host "dl.dagger.io"

  @doc false
  def download(cli_version, opts \\ []) do
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

  defp extract_cli(cli_host, cli_version, bin_path) do
    req = Req.new(url: cli_archive_url(cli_host, cli_version))

    with {:ok, response} <- Req.get(req, raw: true),
         :ok <- verify_checksum(response, cli_host, cli_version) do
      {_, %{body: files}} = Req.Steps.decode_body({req, response})

      {_, dagger_bin} =
        Enum.find(files, fn {filename, _bin} ->
          filename == dagger_bin_in_archive(os())
        end)

      File.write(bin_path, dagger_bin)
    end
  end

  defp verify_checksum(response, cli_host, cli_version) do
    calculated_checksum = :crypto.hash(:sha256, response.body) |> Base.encode16(case: :lower)

    case expected_checksum(cli_host, cli_version) do
      {:ok, expected_checksum} ->
        if :crypto.hash_equals(calculated_checksum, expected_checksum) do
          :ok
        else
          {:error, "checksum mismatch: expected #{expected_checksum}, got #{calculated_checksum}"}
        end

      error ->
        error
    end
  end

  defp expected_checksum(cli_host, cli_version) do
    archive_name = default_cli_archive_name(os(), arch(), cli_version)

    {:ok, checksum_map} = checksum_map(cli_host, cli_version)

    expected_value =
      Enum.find_value(checksum_map, fn {key, value} ->
        if to_string(key) == archive_name, do: value
      end)

    if is_nil(expected_value) do
      {:error, "expected value find error"}
    else
      {:ok, expected_value}
    end
  end

  defp checksum_map(cli_host, cli_version) do
    try do
      with {:ok, response} <- Req.get(checksum_url(cli_host, cli_version)) do
        checksum_map =
          response.body
          |> String.split("\n", trim: true)
          |> Enum.map(&String.split/1)
          |> Enum.map(fn
            [hash, file] ->
              {file, hash}

            list ->
              raise "Invalid checksum line: #{length(list)}"
          end)
          |> Enum.into(%{})

        {:ok, checksum_map}
      end
    rescue
      reason in RuntimeError -> {:error, reason}
    end
  end

  defp dagger_bin_in_archive(:windows), do: ~c"dagger.exe"
  defp dagger_bin_in_archive(_), do: ~c"dagger"

  defp checksum_url(cli_host, cli_version) do
    "https://#{cli_host}/dagger/releases/#{cli_version}/checksums.txt"
  end

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
