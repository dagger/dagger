defmodule ReqAdapter do
  @moduledoc false

  use Dagger.Mod.Object, name: "ReqAdapter"

  defn container_echo(string_arg: String.t()) :: Dagger.Container.t() do
    dag()
    |> Dagger.Client.container()
    |> Dagger.Container.from("alpine:latest")
    |> Dagger.Container.with_exec(~w"echo #{string_arg}")
  end
end
