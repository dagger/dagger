defmodule DaggerIntegrationTest.MixProject do
  use Mix.Project

  def project do
    [
      app: :dagger_integration_test,
      version: "0.1.0",
      elixir: "~> 1.17",
      start_permanent: Mix.env() == :prod,
      elixirc_paths: elixirc_paths(Mix.env()),
      deps: deps()
    ]
  end

  def application do
    [
      extra_applications: [:logger]
    ]
  end

  def elixirc_paths(:test), do: ["lib", "test/support"]
  def elixirc_paths(:dev), do: ["lib"]

  defp deps do
    [
      {:dagger, path: ".."}
    ]
  end
end
