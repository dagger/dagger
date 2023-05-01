defmodule Dagger.MixProject do
  use Mix.Project

  @version "0.2.0-dev"
  @source_url "https://github.com/wingyplus/dagger_ex"

  def project do
    [
      app: :dagger_ex,
      version: @version,
      elixir: "~> 1.14",
      start_permanent: Mix.env() == :prod,
      deps: deps(),
      package: package(),
      docs: docs()
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

  defp package do
    %{
      name: "dagger_ex",
      description: "https://dagger.io SDK for Elixir",
      licenses: ["MIT"],
      links: %{
        "GitHub" => "https://github.com/wingyplus/dagger_ex",
        "Changelog" => "#{@source_url}/blob/v#{@version}/CHANGELOG.md"
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
