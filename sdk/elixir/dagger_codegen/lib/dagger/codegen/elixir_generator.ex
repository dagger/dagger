defmodule Dagger.Codegen.ElixirGenerator do
  @moduledoc """
  Dagger Elixir code generator.
  """

  alias Dagger.Codegen.ElixirGenerator.EnumRenderer
  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.ElixirGenerator.InputRenderer
  alias Dagger.Codegen.ElixirGenerator.ObjectRenderer
  alias Dagger.Codegen.ElixirGenerator.ScalarRenderer

  def generate_scalar(type) do
    ScalarRenderer.render(type)
  end

  def generate_object(type) do
    ObjectRenderer.render(type)
  end

  def generate_input(type) do
    InputRenderer.render(type)
  end

  def generate_enum(type) do
    EnumRenderer.render(type)
  end

  def filename(type) do
    "#{Formatter.format_var_name(type.name)}.ex"
  end

  def format(code) do
    code
    |> IO.iodata_to_binary()
    |> Code.format_string!(code)
  end
end
