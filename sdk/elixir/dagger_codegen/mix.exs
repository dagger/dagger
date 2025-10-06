defmodule Dagger.Codegen.MixProject do
  use Mix.Project

  def project do
    [
      app: :dagger_codegen,
      version: "0.1.0",
      elixir: "~> 1.14",
      start_permanent: Mix.env() == :prod,
      deps: deps(),
      escript: escript(),
      elixirc_paths: elixirc_paths(Mix.env())
    ]
  end

  def application do
    [
      extra_applications: [:logger, :eex]
    ]
  end

  defp escript do
    [main_module: Dagger.Codegen.CLI]
  end

  defp elixirc_paths(:dev), do: ["lib"]
  defp elixirc_paths(:test), do: ["lib", "test/support"]

  defp deps do
    [
      {:mneme, "~> 0.9.0-alpha.1", only: :test}
    ]
  end
end
