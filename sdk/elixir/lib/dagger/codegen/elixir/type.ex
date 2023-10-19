defmodule Dagger.Codegen.Elixir.Type do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Module, as: Mod

  def render_result_type(type) do
    quote do
      {:ok, unquote(type)} | {:error, term()}
    end
  end

  def render_nullable_type(type) do
    quote do
      unquote(type) | nil
    end
  end

  def render_type(%{"kind" => "NON_NULL", "ofType" => type}) do
    render_type(type)
  end

  def render_type(%{"kind" => "OBJECT", "name" => name}) do
    mod_name = Mod.from_name(name)

    quote do
      unquote(mod_name).t()
    end
  end

  def render_type(%{"kind" => "LIST", "ofType" => type}) do
    type = render_type(type)

    quote do
      [unquote(type)]
    end
  end

  def render_type(%{"kind" => "ENUM", "name" => name}) do
    mod_name = Mod.from_name(name)

    quote do
      unquote(mod_name).t()
    end
  end

  def render_type(%{"kind" => "SCALAR", "name" => name}) do
    mod_name = Mod.from_name(name)

    quote do
      unquote(mod_name).t()
    end
  end

  def render_type(%{"kind" => "INPUT_OBJECT", "name" => name}) do
    mod_name = Mod.from_name(name)

    quote do
      unquote(mod_name).t()
    end
  end
end
