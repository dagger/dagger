# This file generated by `dagger_codegen`. Please DO NOT EDIT.
defmodule Dagger.Terminal do
  @moduledoc "An interactive terminal that clients can connect to."

  use Dagger.Core.QueryBuilder

  @derive Dagger.ID
  @derive Dagger.Sync
  defstruct [:selection, :client]

  @type t() :: %__MODULE__{}

  @doc "A unique identifier for this Terminal."
  @spec id(t()) :: {:ok, Dagger.TerminalID.t()} | {:error, term()}
  def id(%__MODULE__{} = terminal) do
    selection =
      terminal.selection |> select("id")

    execute(selection, terminal.client)
  end

  @doc """
  Forces evaluation of the pipeline in the engine.

  It doesn't run the default command if no exec has been set.
  """
  @spec sync(t()) :: {:ok, Dagger.Terminal.t()} | {:error, term()}
  def sync(%__MODULE__{} = terminal) do
    selection =
      terminal.selection |> select("sync")

    with {:ok, id} <- execute(selection, terminal.client) do
      {:ok,
       %Dagger.Terminal{
         selection:
           query()
           |> select("loadTerminalFromID")
           |> arg("id", id),
         client: terminal.client
       }}
    end
  end
end
