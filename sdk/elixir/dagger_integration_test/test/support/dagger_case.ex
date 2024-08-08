defmodule Dagger.Case do
  use ExUnit.CaseTemplate

  setup do
    dag = Dagger.connect!()
    on_exit(fn -> Dagger.close(dag) end)
    %{dag: dag}
  end
end
