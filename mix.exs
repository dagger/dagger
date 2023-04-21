defmodule Dagger.MixProject do
  use Mix.Project

  def project do
    [
      app: :dagger_ex,
      version: "0.1.0-dev",
      elixir: "~> 1.14",
      start_permanent: Mix.env() == :prod,
      deps: deps()
    ]
  end

  def application do
    [
      extra_applications: [:logger]
    ]
  end

  defp deps do
    [
      {:req, "~> 0.3"},
      {:absinthe_client, "~> 0.1"},
      {:ex_doc, "~> 0.27", only: :dev, runtime: false}
    ]
  end
end
