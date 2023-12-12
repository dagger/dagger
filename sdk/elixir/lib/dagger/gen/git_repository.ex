# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.GitRepository do
  @moduledoc "A git repository."
  use Dagger.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "Returns details on one branch.\n\n## Required Arguments\n\n* `name` - Branch's name (e.g., \"main\")."
    @spec branch(t(), Dagger.String.t()) :: Dagger.GitRef.t()
    def branch(%__MODULE__{} = git_repository, name) do
      selection = select(git_repository.selection, "branch")
      selection = arg(selection, "name", name)
      %Dagger.GitRef{selection: selection, client: git_repository.client}
    end
  )

  (
    @doc "Returns details on one commit.\n\n## Required Arguments\n\n* `id` - Identifier of the commit (e.g., \"b6315d8f2810962c601af73f86831f6866ea798b\")."
    @spec commit(t(), Dagger.String.t()) :: Dagger.GitRef.t()
    def commit(%__MODULE__{} = git_repository, id) do
      selection = select(git_repository.selection, "commit")
      selection = arg(selection, "id", id)
      %Dagger.GitRef{selection: selection, client: git_repository.client}
    end
  )

  (
    @doc "Retrieves the content-addressed identifier of the git repository."
    @spec id(t()) :: {:ok, Dagger.GitRepositoryID.t()} | {:error, term()}
    def id(%__MODULE__{} = git_repository) do
      selection = select(git_repository.selection, "id")
      execute(selection, git_repository.client)
    end
  )

  (
    @doc "Returns details on one tag.\n\n## Required Arguments\n\n* `name` - Tag's name (e.g., \"v0.3.9\")."
    @spec tag(t(), Dagger.String.t()) :: Dagger.GitRef.t()
    def tag(%__MODULE__{} = git_repository, name) do
      selection = select(git_repository.selection, "tag")
      selection = arg(selection, "name", name)
      %Dagger.GitRef{selection: selection, client: git_repository.client}
    end
  )
end
