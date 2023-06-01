defmodule Dagger.Session do
  @moduledoc false

  def start(bin, engine_conn_pid, opts) do
    workdir = opts[:workdir] || File.cwd!()
    logger = logger_from(opts[:log_output])

    args = ["--workdir", Path.expand(workdir)]

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
        case Jason.decode(log_line) do
          {:ok, session} ->
            send(engine_conn_pid, {self(), session})
            :ok

          {:error, _} ->
            logger.(log_line)
            wait_for_session(port, engine_conn_pid, logger)
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
