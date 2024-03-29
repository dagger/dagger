defmodule Main do
  require EEx

  # TODO: group all `run` functions to only one clause for performance.

  def run(["gen_mix_exs", module]) do
    module = normalize_name(module)

    render_mix_exs(
      module: Macro.camelize(module),
      application: ":#{Macro.underscore(module)}"
    )
    |> IO.puts()
  end

  def run(["gen_module", module]) do
    module = normalize_name(module)

    render_module(module: Macro.camelize(module))
    |> IO.puts()
  end

  def run(["gen_mix_task", module]) do
    render_mix_task(application: ":#{Macro.underscore(module)}")
    |> IO.puts()
  end

  def run(["gen_application", module]) do
    module = normalize_name(module)

    render_application(module: Macro.camelize(module))
    |> IO.puts()
  end

  defp normalize_name(module) do
    String.replace(module, "-", "_")
  end

  @mix_exs """
  defmodule <%= @module %>.MixProject do
    use Mix.Project

    def project do
      [
        app: <%= @application %>,
        version: "0.1.0",
        elixir: "~> 1.16",
        start_permanent: Mix.env() == :prod,
        deps: deps()
      ]
    end

    def application do
      [
        extra_applications: [:logger],
        mod: {<%= @module %>.Application, []}
      ]
    end

    defp deps do
      [
        {:dagger, path: "../dagger"}
      ]
    end
  end
  """

  EEx.function_from_string(:def, :render_mix_exs, @mix_exs, [:assigns])

  @module_ex """
  defmodule <%= @module %> do
    @moduledoc \"\"\"
    Documentation for `<%= @module %>`.
    \"\"\"

    use Dagger.ModuleRuntime, name: "<%= @module %>"

    defstruct [:dag]

    @function [
      args: [
        string_arg: [type: :string]
      ],
      return: Dagger.Container
    ]
    def container_echo(self, args) do
      self.dag
      |> Dagger.Client.container()
      |> Dagger.Container.from("alpine:latest")
      |> Dagger.Container.with_exec(~w"echo \#{args.string_arg}")
    end

    @function [
      args: [
        directory_arg: [type: Dagger.Directory],
        pattern: [type: :string]
      ],
      return: :string
    ]
    def grep_dir(self, %{directory_arg: directory, pattern: pattern}) do
      self.dag
      |> Dagger.Client.container()
      |> Dagger.Container.from("alpine:latest")
      |> Dagger.Container.with_mounted_directory("/mnt", directory)
      |> Dagger.Container.with_workdir("/mnt")
      |> Dagger.Container.with_exec(["grep", "-R", pattern, "."])
      |> Dagger.Container.stdout()
    end
  end
  """

  EEx.function_from_string(:def, :render_module, @module_ex, [:assigns])

  @mix_task_ex """
  defmodule Mix.Tasks.Dagger.Invoke do
    use Mix.Task

    def run(_args) do
      Mix.ensure_application!(:inets)
      Application.ensure_all_started(:dagger)
      Application.ensure_all_started(<%= @application %>)
      Dagger.ModuleRuntime.invoke()
    end
  end
  """

  EEx.function_from_string(:def, :render_mix_task, @mix_task_ex, [:assigns])

  @application_ex """
  defmodule <%= @module %>.Application do
    # See https://hexdocs.pm/elixir/Application.html
    # for more information on OTP Applications
    @moduledoc false

    use Application

    @impl true
    def start(_type, _args) do
      children = [
        Dagger.ModuleRuntime.Registry, 
        <%= @module %>
      ]

      # See https://hexdocs.pm/elixir/Supervisor.html
      # for other strategies and supported options
      opts = [strategy: :one_for_one, name: Potato.Supervisor]
      Supervisor.start_link(children, opts)
    end
  end
  """

  EEx.function_from_string(:def, :render_application, @application_ex, [:assigns])
end

Main.run(System.argv())
