defmodule Dagger.Session do
  @moduledoc false

  def start(bin, engine_conn_pid, logger) do
    port = Port.open({:spawn_executable, bin}, [:binary, :stderr_to_stdout, args: ["session"]])

    wait_for_session(port, engine_conn_pid)
    log_polling(port, logger)
  end

  defp wait_for_session(port, engine_conn_pid) do
    receive do
      {^port, {:data, session}} ->
        send(engine_conn_pid, {self(), Jason.decode!(session)})
    end
  end

  defp log_polling(port, logger) do
    receive do
      {^port, {:data, log_line}} ->
        logger.(log_line)
    end

    log_polling(port, logger)
  end
end

defmodule Dagger.StdoutLogger do
  @moduledoc false

  # Log the entry into standard output.

  def log(line) do
    IO.write(line)
  end
end
