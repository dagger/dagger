defmodule Dagger.ModuleRuntime.Registry do
  @moduledoc """
  TBD.
  """

  use Agent

  def start_link(opts) do
    name = opts[:name] || __MODULE__
    Agent.start_link(fn -> %{} end, name: name)
  end

  @doc """
  Register a module.
  """
  def register(pid \\ __MODULE__, module) do
    name = Dagger.ModuleRuntime.Module.name_for(module)
    fun = fn modules -> Map.put(modules, name, module) end
    Agent.update(pid, fun)
  end

  @doc """
  Get all registered modules.
  """
  def all(pid \\ __MODULE__) do
    fun = fn modules -> Enum.map(modules, fn {_, module} -> module end) end
    Agent.get(pid, fun)
  end

  def get(pid \\ __MODULE__, name) when is_binary(name) do
    fun = fn modules -> modules[name] end
    Agent.get(pid, fun)
  end
end
