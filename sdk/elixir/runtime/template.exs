defmodule Main do
  require EEx

  def run(["generate", mod]) do
    File.mkdir_p!(Path.join([mod, "lib", "mix", "tasks"]))

    # Go strcase convert a module that end with digits to `<string>_<digit>` but 
    # Elixir convert back to `<string><digit>`. 
    mod_name = mod |> Macro.camelize() |> Macro.underscore()

    mix_exs =
      render_mix_exs(
        module: Macro.camelize(mod),
        application: atom(Macro.underscore(mod))
      )

    module = render_module(module: Macro.camelize(mod))
    application_module = render_application(module: Macro.camelize(mod))
    mix_task = render_mix_task(application: atom(Macro.underscore(mod)))

    File.write!(Path.join([mod, "mix.exs"]), mix_exs)
    File.write!(Path.join([mod, "lib", "#{mod_name}.ex"]), module)
    File.write!(Path.join([mod, "lib", mod_name, "application.ex"]), application_module)
    File.write!(Path.join([mod, "lib", "mix", "tasks", "dagger.invoke.ex"]), mix_task)
  end

  defp atom(string), do: ":#{string}"

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
        {:dagger, path: "../dagger_sdk"}
      ]
    end
  end
  """

  EEx.function_from_string(:def, :render_mix_exs, @mix_exs, [:assigns])

  @module_ex """
  defmodule <%= @module %> do
    @moduledoc \"\"\"
    A generated module for Main functions

    This module has been generated via dagger init and serves as a reference to
    basic module structure as you get started with Dagger.

    Two functions have been pre-created. You can modify, delete, or add to them,
    as needed. They demonstrate usage of arguments and return types using simple
    echo and grep commands. The functions can be called from the dagger CLI or
    from one of the SDKs.

    The first line in this comment block is a short description line and the
    rest is a long description with more detail on the module's purpose or usage,
    if appropriate. All modules should have a short description.
    \"\"\"

    use Dagger.Mod, name: "<%= @module %>"

    defstruct [:dag]

    @function [
      args: [
        string_arg: [type: :string]
      ],
      return: Dagger.Container
    ]
    @doc \"\"\"
    Returns a container that echoes whatever string argument is provided.
    \"\"\"
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
    @doc \"\"\"
    Returns lines that match a pattern in the files of the provided Directory.
    \"\"\"
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
      Dagger.Mod.invoke()
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
        Dagger.Mod.Registry, 
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
