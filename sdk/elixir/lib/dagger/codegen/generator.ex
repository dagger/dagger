defmodule Dagger.Codegen.Generator do
  @moduledoc false

  def generate() do
    {:ok, client} = Dagger.Core.Client.connect()

    {:ok, %{"data" => data}} =
      Dagger.Core.Client.query(client, Dagger.Codegen.Introspection.query())

    Dagger.Codegen.Compiler.compile(data)
  end
end
