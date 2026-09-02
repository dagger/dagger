defmodule Dagger.Core.EngineConnTest do
  use ExUnit.Case, async: false

  alias Dagger.Core.EngineConn

  @env_names ["DAGGER_NESTING", "DAGGER_SESSION_PORT", "DAGGER_SESSION_TOKEN"]

  setup do
    previous = Map.new(@env_names, &{&1, System.get_env(&1)})

    on_exit(fn ->
      Enum.each(previous, fn
        {name, nil} -> System.delete_env(name)
        {name, value} -> System.put_env(name, value)
      end)
    end)

    Enum.each(@env_names, &System.delete_env/1)
    :ok
  end

  test "independent sessions provision a CLI without an inherited token" do
    System.put_env("DAGGER_NESTING", "INDEPENDENT_SESSIONS")
    System.put_env("DAGGER_SESSION_PORT", "1234")

    assert {:error, :no_session} = EngineConn.from_session_env([])
  end

  test "explicit nesting validates the inherited environment" do
    System.put_env("DAGGER_NESTING", "INDEPENDENT_SESSIONS")
    assert {:error, {:missing_session_port, "INDEPENDENT_SESSIONS"}} =
             EngineConn.from_session_env([])

    System.put_env("DAGGER_NESTING", "UNKNOWN")
    assert {:error, {:unknown_dagger_nesting, "UNKNOWN"}} = EngineConn.from_session_env([])
  end
end
