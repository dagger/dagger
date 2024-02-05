# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.GeneratedCode do
  @moduledoc "The result of running an SDK's codegen."
  use Dagger.Core.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc "The directory containing the generated code."
    @spec code(t()) :: Dagger.Directory.t()
    def code(%__MODULE__{} = generated_code) do
      selection = select(generated_code.selection, "code")
      %Dagger.Directory{selection: selection, client: generated_code.client}
    end
  )

  (
    @doc "A unique identifier for this GeneratedCode."
    @spec id(t()) :: {:ok, Dagger.GeneratedCodeID.t()} | {:error, term()}
    def id(%__MODULE__{} = generated_code) do
      selection = select(generated_code.selection, "id")
      execute(selection, generated_code.client)
    end
  )

  (
    @doc "List of paths to mark generated in version control (i.e. .gitattributes)."
    @spec vcs_generated_paths(t()) :: {:ok, [Dagger.String.t()]} | {:error, term()}
    def vcs_generated_paths(%__MODULE__{} = generated_code) do
      selection = select(generated_code.selection, "vcsGeneratedPaths")
      execute(selection, generated_code.client)
    end
  )

  (
    @doc "List of paths to ignore in version control (i.e. .gitignore)."
    @spec vcs_ignored_paths(t()) :: {:ok, [Dagger.String.t()]} | {:error, term()}
    def vcs_ignored_paths(%__MODULE__{} = generated_code) do
      selection = select(generated_code.selection, "vcsIgnoredPaths")
      execute(selection, generated_code.client)
    end
  )

  (
    @doc "Set the list of paths to mark generated in version control.\n\n## Required Arguments\n\n* `paths` -"
    @spec with_vcs_generated_paths(t(), [Dagger.String.t()]) :: Dagger.GeneratedCode.t()
    def with_vcs_generated_paths(%__MODULE__{} = generated_code, paths) do
      selection = select(generated_code.selection, "withVCSGeneratedPaths")
      selection = arg(selection, "paths", paths)
      %Dagger.GeneratedCode{selection: selection, client: generated_code.client}
    end
  )

  (
    @doc "Set the list of paths to ignore in version control.\n\n## Required Arguments\n\n* `paths` -"
    @spec with_vcs_ignored_paths(t(), [Dagger.String.t()]) :: Dagger.GeneratedCode.t()
    def with_vcs_ignored_paths(%__MODULE__{} = generated_code, paths) do
      selection = select(generated_code.selection, "withVCSIgnoredPaths")
      selection = arg(selection, "paths", paths)
      %Dagger.GeneratedCode{selection: selection, client: generated_code.client}
    end
  )
end
