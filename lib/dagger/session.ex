defmodule Dagger.Session do
  @moduledoc false

  def start(bin, engine_conn_pid, logger) do
    port = Port.open({:spawn_executable, bin}, [:binary, :stderr_to_stdout, args: ["session"]])

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
end

defmodule Dagger.StdoutLogger do
  @moduledoc false

  # Log the entry into standard output.

  def log(line) do
    IO.write(line)
  end
end
