defmodule Dagger.Session do
  @moduledoc false

  @sdk_version Mix.Project.config() |> Keyword.fetch!(:version)

  def start(bin, engine_conn_pid, opts) do
    workdir = opts[:workdir] || File.cwd!()
    logger = logger_from(opts[:log_output])

    args = [
      "--workdir",
      Path.expand(workdir),
      "--label",
      "dagger.io/sdk.name:elixir",
      "--label",
      "dagger.io/sdk.version:#{@sdk_version}"
    ]

    port =
      Port.open({:spawn_executable, bin}, [
        :binary,
        :stderr_to_stdout,
        args: ["session" | args]
      ])

    with :ok <- wait_for_session(port, engine_conn_pid, logger) do
      log_polling(port, logger)
    end
  end

  def stop(pid) do
    send(pid, :quit)
  end

  defp wait_for_session(port, engine_conn_pid, logger) do
    receive do
      {^port, {:data, log_line}} ->
        logger.(log_line)

        result =
          String.split(log_line, "\n", trim: true)
          |> Enum.map(&Jason.decode/1)
          |> Enum.find(fn
            {:ok, _} -> true
            {:error, _} -> false
          end)

        case result do
          nil ->
            wait_for_session(port, engine_conn_pid, logger)

          {:ok, session} ->
            send(engine_conn_pid, {self(), session})
            :ok
        end

      :quit ->
        true = Port.close(port)
        {:error, :quit}
    end
  end

  defp log_polling(port, logger) do
    receive do
      {^port, {:data, log_line}} ->
        logger.(log_line)
        log_polling(port, logger)

      :quit ->
        true = Port.close(port)
        :ok
    end
  end

  defp logger_from(log_output) do
    fn msg -> IO.binwrite(log_output, msg) end
  end
end
