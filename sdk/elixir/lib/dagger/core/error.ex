defmodule Dagger.Core.ExecError do
  @moduledoc """
  API error from an exec operation.
  """

  defexception [:original_error, :cmd, :exit_code, :stdout, :stderr]

  def from_map(map) do
    %__MODULE__{
      cmd: map["cmd"],
      exit_code: map["exitCode"],
      stdout: map["stdout"],
      stderr: map["stderr"]
    }
  end

  def with_original_error(exec_error, error) do
    %{exec_error | original_error: error}
  end

  @impl true
  def message(exception) do
    Exception.message(exception.original_error)
  end
end
