defmodule Dagger.Codegen do
  @moduledoc """
  Functions for generating code from Dagger GraphQL.
  """

  def generate(generator, introspection_schema) do
    generator.generate(introspection_schema)
  end
end
