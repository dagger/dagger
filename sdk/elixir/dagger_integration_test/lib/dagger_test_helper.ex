defmodule Dagger.TestHelper do
  @moduledoc """
  Dagger integration tests suite for Dagger Elixir SDK. 
  """

  def sdk(), do: System.fetch_env!("DAG_EX_SDK")

  def dagger_cli_path() do
    System.find_executable("dagger") || raise "Cannot find `dagger` binary"
  end

  def dagger_cli_file(dag) do
    dag
    |> Dagger.Client.host()
    |> Dagger.Host.file(dagger_cli_path())
  end

  def dagger_cli_base(dag) do
    dag
    |> Dagger.Client.container()
    |> Dagger.Container.from("golang:1.22.5-alpine")
    |> Dagger.Container.with_mounted_file("/bin/dagger", dagger_cli_file(dag))
    |> Dagger.Container.with_workdir("/work")
  end

  def dagger_init(container), do: dagger_init(container, [])

  def dagger_init(container, args) do
    dagger_init(container, "", args)
  end

  def dagger_init(container, mod_path, args) do
    dagger_init(container, mod_path, args, sdk())
  end

  def dagger_init(container, mod_path, args, sdk) do
    exec_args = ["init", "--sdk=#{sdk}"]

    exec_args =
      if length(args) == 0 do
        exec_args ++ ["--name=test"]
      end

    exec_args =
      if mod_path != "" do
        exec_args ++ ["--source=#{mod_path}", mod_path]
      else
        exec_args ++ ["--source=."]
      end

    dagger_exec(container, exec_args)
  end

  def dagger_exec(container, args) do
    container
    |> Dagger.Container.with_exec(["dagger", "--debug" | args],
      experimental_privileged_nesting: true
    )
  end

  def dagger_call(container, args), do: dagger_call(container, "", args)

  def dagger_call(container, mod_path, args) do
    exec_args = ["dagger", "--debug", "call"]

    exec_args =
      if mod_path != "" do
        exec_args ++ ["-m", mod_path]
      else
        exec_args
      end

    container
    |> Dagger.Container.with_exec(exec_args ++ args,
      use_entrypoint: true,
      experimental_privileged_nesting: true
    )
  end

  def dagger_query(container, query), do: dagger_query(container, "", query)

  def dagger_query(container, mod_path, query) do
    exec_args = ["dagger", "--debug", "query"]

    exec_args =
      if mod_path != "" do
        exec_args ++ ["-m", mod_path]
      else
        exec_args
      end

    container
    |> Dagger.Container.with_exec(exec_args,
      stdin: query,
      experimental_privileged_nesting: true
    )
  end

  def dagger_with_source(container, path, contents) do
    container
    |> Dagger.Container.with_new_file(path, contents)
  end

  defdelegate stdout(container), to: Dagger.Container
end
