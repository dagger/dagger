defmodule Dagger.DagCase do
  @moduledoc false

  use ExUnit.CaseTemplate

  setup_all do
    start_supervised!(Dagger.Global)
    %{dag: Dagger.Global.dag()}
  end
end
