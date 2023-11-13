defmodule Dagger.Codegen.Generator do
  @moduledoc false

  def generate() do
    {:ok, client} = Dagger.Core.Client.connect()

    {:ok, %{status: 200, body: resp}} =
      Dagger.Core.Client.query(client, Dagger.Codegen.Introspection.query())

    Dagger.Codegen.Compiler.compile(resp["data"])
  end
end
