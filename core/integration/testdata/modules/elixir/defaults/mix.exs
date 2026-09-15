defmodule Template.MixProject do
  use Mix.Project

  def project do
    [
      app: :defaults,
      version: "0.1.0",
      elixir: "~> 1.17",
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
      {:dagger, path: "./dagger_sdk"}
    ]
  end
end
