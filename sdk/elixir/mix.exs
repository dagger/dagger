defmodule Dagger.MixProject do
  use Mix.Project

  @version "0.0.0"
  @source_url "https://github.com/dagger/dagger"

  def project do
    [
      app: :dagger,
      version: @version,
      elixir: "~> 1.14",
      start_permanent: Mix.env() == :prod,
      deps: deps(),
      package: package(),
      docs: docs(),
      aliases: aliases()
    ]
  end

  def application do
    [
      extra_applications: [:logger, :public_key, :ssl]
    ]
  end

  defp deps do
    [
      {:jason, "~> 1.4"},
      {:nimble_options, "~> 1.0"},
      {:nestru, "~> 0.3"},
      {:opentelemetry_api, "~> 1.3"},
      {:opentelemetry_exporter, "~> 1.7"},
      {:ex_doc, "~> 0.27", only: :dev, runtime: false},
      {:credo, "~> 1.7", only: [:dev, :test], runtime: false}
    ]
  end

  defp aliases do
    [
      lint: ["format --check-formatted", "credo"]
    ]
  end

  defp package do
    %{
      name: "dagger",
      description: "Dagger SDK for Elixir",
      licenses: ["Apache-2.0"],
      links: %{
        "GitHub" => @source_url,
        "Changelog" => "#{@source_url}/releases/tag/sdk%2Felixir%2Fv#{@version}"
      }
    }
  end

  defp docs do
    [
      source_ref: "v#{@version}",
      source_url: @source_url,
      main: "Dagger",
      extras: ["getting_started.livemd"]
    ]
  end
end
