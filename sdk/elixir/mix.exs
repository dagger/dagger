defmodule Dagger.MixProject do
  use Mix.Project

  @version File.read!("VERSION") |> String.trim()
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
      extra_applications: [:logger]
    ]
  end

  defp deps do
    [
      {:req, "~> 0.3"},
      {:absinthe_client, "~> 0.1"},
      {:nimble_options, "~> 1.0"},
      {:nestru, "~> 0.3"},
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
      },
      files: [
        "lib",
        "priv",
        ".formatter.exs",
        "mix.exs",
        "README*",
        "LICENSE*",
        "CHANGELOG*",
        "VERSION"
      ]
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
