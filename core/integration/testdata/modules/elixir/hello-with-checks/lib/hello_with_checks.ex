defmodule HelloWithChecks do
  @moduledoc false

  use Dagger.Mod.Object, name: "HelloWithChecks"

  @check true
  defn passing_check() :: Dagger.Void.t() do
    :ok
  end

  @check true
  defn failing_check() :: Dagger.Void.t() do
    raise "this check always fails"
  end

  @check true
  defn passing_container() :: Dagger.Container.t() do
    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from("alpine:3")
    |> Dagger.Container.with_exec(["sh", "-c", "exit 0"])
  end

  @check true
  defn failing_container() :: Dagger.Container.t() do
    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from("alpine:3")
    |> Dagger.Container.with_exec(["sh", "-c", "exit 1"])
  end
end
