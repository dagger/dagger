defmodule Dagger.MixProject do
  use Mix.Project

  @version "0.1.0"

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
      licences: ["MIT"],
      links: %{"GitHub" => "https://github.com/wingyplus/dagger_ex"}
    }
  end

  defp docs do
    [
      source_ref: "v#{@version}",
      main: "Dagger",
      extras: ["getting_started.livemd"]
    ]
  end
end
