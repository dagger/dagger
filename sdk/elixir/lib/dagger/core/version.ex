defmodule Dagger.Core.Version do
  @moduledoc false

  @dagger_cli_version "0.16.2"

  def engine_version(), do: @dagger_cli_version
end
