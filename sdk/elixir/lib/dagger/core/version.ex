defmodule Dagger.Core.Version do
  @moduledoc false

  @dagger_cli_version "1.0.0-beta.26"

  def engine_version(), do: @dagger_cli_version
end
