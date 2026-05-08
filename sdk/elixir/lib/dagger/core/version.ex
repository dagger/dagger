defmodule Dagger.Core.Version do
  @moduledoc false

  @dagger_cli_version "0.20.6"

  def engine_version(), do: @dagger_cli_version
end
