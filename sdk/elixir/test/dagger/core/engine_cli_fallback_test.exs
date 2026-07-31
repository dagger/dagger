defmodule Dagger.Core.EngineCLIFallbackTest do
  use ExUnit.Case, async: false

  @moduletag :tmp_dir

  alias Dagger.Core.Engine.Downloader
  alias Dagger.Core.EngineConn

  defmodule UnavailableDownloader do
    def download(_cli_version) do
      {:error,
       {:cli_release_unavailable,
        {:failed_to_download_checksums, "https://example.test/checksums.txt", {:http_status, 403}}}}
    end
  end

  defmodule ProcessDownloader do
    def download(_cli_version) do
      {:ok, Process.get(:downloaded_cli_path)}
    end
  end

  setup do
    path = System.get_env("PATH")

    on_exit(fn ->
      if path do
        System.put_env("PATH", path)
      else
        System.delete_env("PATH")
      end
    end)

    :ok
  end

  describe "release availability" do
    # Missing checksums mean the release is absent; a missing archive may be a
    # partial or broken release, so it must remain fatal.
    for status <- [403, 404] do
      test "marks checksums returning #{status} as unavailable" do
        status = unquote(status)
        url = start_server(status)

        assert {:error,
                {:cli_release_unavailable,
                 {:failed_to_download_checksums, ^url, {:http_status, ^status}}}} =
                 Downloader.checksum_map(url)
      end

      test "does not mark an archive returning #{status} as unavailable", %{tmp_dir: tmp_dir} do
        status = unquote(status)
        url = start_server(status)

        assert {:error, {:failed_to_download_cli, ^url, {:http_status, ^status}}} =
                 Downloader.extract_cli(url, "unused", Path.join(tmp_dir, "dagger"))
      end
    end

    test "checks release availability before downloading the archive", %{tmp_dir: tmp_dir} do
      checksums_url = start_server(500)

      assert {:error, {:failed_to_download_checksums, ^checksums_url, {:http_status, 500}}} =
               Downloader.download("unreleased",
                 cache_dir: tmp_dir,
                 checksums_url: checksums_url,
                 archive_url: "not a valid URL"
               )
    end
  end

  describe "fallback to the local CLI" do
    test "uses the dagger CLI in PATH and emits a compatibility warning", %{tmp_dir: tmp_dir} do
      bin_path = create_dagger_executable(tmp_dir)
      System.put_env("PATH", tmp_dir)
      {:ok, logs} = StringIO.open("")

      download_error = cli_release_unavailable_error()

      assert {:ok, ^bin_path} =
               EngineConn.fallback_to_local_cli(download_error, "unreleased", logs)

      assert {_input, output} = StringIO.contents(logs)

      assert output ==
               "CLI version unreleased is unavailable; using #{bin_path} from PATH " <>
                 "(version compatibility is not guaranteed).\n"
    end

    test "returns unrelated download errors without falling back", %{tmp_dir: tmp_dir} do
      _bin_path = create_dagger_executable(tmp_dir)
      System.put_env("PATH", tmp_dir)
      download_error = {:failed_to_download_checksums, "checksums.txt", {:http_status, 500}}

      assert {:error, ^download_error} =
               EngineConn.fallback_to_local_cli(download_error, "unreleased")
    end

    test "preserves the download and PATH errors when no CLI is found", %{tmp_dir: tmp_dir} do
      System.put_env("PATH", tmp_dir)
      download_error = cli_release_unavailable_error()

      assert {:error,
              {:cli_fallback_failed, ^download_error,
               {:dagger_cli_not_found, "dagger CLI not found in PATH", :no_executable}}} =
               result =
               EngineConn.fallback_to_local_cli(download_error, "unreleased")

      rendered = inspect(result)
      assert rendered =~ "cli_release_unavailable"
      assert rendered =~ "dagger CLI not found in PATH"
    end

    test "preserves the download and fallback session errors", %{tmp_dir: tmp_dir} do
      bin_path = create_dagger_executable(tmp_dir)
      System.put_env("PATH", tmp_dir)
      {:ok, logs} = StringIO.open("")
      download_error = cli_release_unavailable_error()
      session_error = {:error, {:session_start_failed, :boom}}
      start_session = fn ^bin_path, _opts -> session_error end

      assert {:error,
              {:cli_fallback_failed, ^download_error,
               {:failed_to_use_cli_from_path, ^bin_path, ^session_error}}} =
               result =
               EngineConn.from_remote_cli(
                 [log_output: logs],
                 UnavailableDownloader,
                 start_session
               )

      rendered = inspect(result)
      assert rendered =~ "cli_release_unavailable"
      assert rendered =~ "session_start_failed"
    end
  end

  describe "session startup lifecycle" do
    test "returns a visible error when the session process fails before the handshake", %{
      tmp_dir: tmp_dir
    } do
      missing_bin_path = Path.join(tmp_dir, "missing-dagger")
      Process.put(:downloaded_cli_path, missing_bin_path)
      on_exit(fn -> Process.delete(:downloaded_cli_path) end)

      assert {:error, {:session_start_failed, reason}} =
               EngineConn.from_remote_cli(
                 [connect_timeout: 1_000, log_output: :stderr],
                 ProcessDownloader
               )

      assert inspect(reason) =~ "enoent"
    end

    test "links the session process after a successful handshake", %{tmp_dir: tmp_dir} do
      bin_path = create_session_executable(tmp_dir)
      test_pid = self()
      {:ok, logs} = StringIO.open("")

      {caller_pid, caller_ref} =
        spawn_monitor(fn ->
          Process.put(:downloaded_cli_path, bin_path)

          {:ok, conn} =
            EngineConn.from_remote_cli(
              [connect_timeout: 1_000, log_output: logs],
              ProcessDownloader
            )

          send(test_pid, {:session_started, self(), conn.session_pid})
          Process.sleep(:infinity)
        end)

      assert_receive {:session_started, ^caller_pid, session_pid}, 1_000
      Process.exit(session_pid, :kill)
      assert_receive {:DOWN, ^caller_ref, :process, ^caller_pid, :killed}, 1_000
    end
  end

  defp cli_release_unavailable_error do
    {:cli_release_unavailable,
     {:failed_to_download_checksums, "https://example.test/checksums.txt", {:http_status, 403}}}
  end

  defp create_dagger_executable(tmp_dir) do
    bin_name = if match?({:win32, _}, :os.type()), do: "dagger.exe", else: "dagger"
    bin_path = Path.join(tmp_dir, bin_name)
    File.write!(bin_path, "")
    File.chmod!(bin_path, 0o700)
    bin_path
  end

  defp create_session_executable(tmp_dir) do
    bin_path = Path.join(tmp_dir, "fake-dagger")

    File.write!(
      bin_path,
      "#!/bin/sh\n" <>
        "echo '{\"port\":1234,\"session_token\":\"token\"}'\n" <>
        "while :; do sleep 60; done\n"
    )

    File.chmod!(bin_path, 0o700)
    bin_path
  end

  defp start_server(status, body \\ "") do
    {:ok, listen_socket} =
      :gen_tcp.listen(0, [
        :binary,
        packet: :raw,
        active: false,
        reuseaddr: true,
        ip: {127, 0, 0, 1}
      ])

    {:ok, {{127, 0, 0, 1}, port}} = :inet.sockname(listen_socket)

    server_pid =
      spawn(fn ->
        {:ok, socket} = :gen_tcp.accept(listen_socket)
        {:ok, _request} = :gen_tcp.recv(socket, 0, 5_000)

        response =
          "HTTP/1.1 #{status} Test\r\n" <>
            "content-length: #{byte_size(body)}\r\n" <>
            "content-type: application/octet-stream\r\n" <>
            "connection: close\r\n\r\n" <> body

        :ok = :gen_tcp.send(socket, response)
        :gen_tcp.close(socket)
      end)

    on_exit(fn ->
      :gen_tcp.close(listen_socket)

      if Process.alive?(server_pid) do
        Process.exit(server_pid, :kill)
      end
    end)

    "http://127.0.0.1:#{port}"
  end
end
