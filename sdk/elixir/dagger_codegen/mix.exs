defmodule Dagger.Codegen.MixProject do
  use Mix.Project

  def project do
    [
      app: :dagger_codegen,
      version: "0.1.0",
      elixir: "~> 1.14",
      start_permanent: Mix.env() == :prod,
      deps: deps(),
      escript: escript()
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

  defp deps do
    [
      {:jason, "~> 1.0"},
      {:nestru, "~> 0.3"},
      {:stream_data, "~> 0.6", only: :test}
    ]
  end
end
