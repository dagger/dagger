defmodule Dagger.Codegen.Introspection do
  @moduledoc false

  @introspection_query_path Path.join([
                              Application.app_dir(:dagger_ex),
                              "priv",
                              "introspection.graphql"
                            ])
  @external_resource @introspection_query_path

  def query() do
    File.read!(@introspection_query_path)
  end
end
