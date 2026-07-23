defmodule SelfCalls do
  @moduledoc false

  use Dagger.Mod.Object, name: "SelfCalls"

  defn container_echo(string_arg: {String.t(), default: "Hello Self Calls"}) :: Dagger.Container.t() do
    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from("alpine:latest")
    |> Dagger.Container.with_exec(["echo", string_arg])
  end

  defn print(string_arg: String.t()) :: String.t() do
    {:ok, result} =
      dag()
      |> Dagger.Client.self_calls()
      |> Dagger.SelfCalls.container_echo(string_arg: string_arg)
      |> Dagger.Container.stdout()

    result
  end

  defn print_default() :: String.t() do
    {:ok, result} =
      dag()
      |> Dagger.Client.self_calls()
      |> Dagger.SelfCalls.container_echo()
      |> Dagger.Container.stdout()

    result
  end
end
