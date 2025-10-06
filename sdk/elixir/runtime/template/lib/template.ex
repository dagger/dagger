defmodule {{ .ModName }} do
  @moduledoc """
  A generated module for Main functions

  This module has been generated via dagger init and serves as a reference to
  basic module structure as you get started with Dagger.

  Two functions have been pre-created. You can modify, delete, or add to them,
  as needed. They demonstrate usage of arguments and return types using simple
  echo and grep commands. The functions can be called from the dagger CLI or
  from one of the SDKs.

  The first line in this comment block is a short description line and the
  rest is a long description with more detail on the module's purpose or usage,
  if appropriate. All modules should have a short description.
  """

  use Dagger.Mod.Object, name: "{{ .ModName }}"

  @doc """
  Returns a container that echoes whatever string argument is provided.
  """
  defn container_echo(string_arg: String.t()) :: Dagger.Container.t() do
    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from("alpine:latest")
    |> Dagger.Container.with_exec(~w"echo #{string_arg}")
  end

  @doc """
  Returns lines that match a pattern in the files of the provided Directory.
  """
  defn grep_dir(directory_arg: Dagger.Directory.t(), pattern: String.t()) :: String.t() do
    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from("alpine:latest")
    |> Dagger.Container.with_mounted_directory("/mnt", directory_arg)
    |> Dagger.Container.with_workdir("/mnt")
    |> Dagger.Container.with_exec(["grep", "-R", pattern, "."])
    |> Dagger.Container.stdout()
  end
end
