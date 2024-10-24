defmodule Main do
  require EEx

  def run(["generate", mod]) do
    File.mkdir_p!(Path.join([mod, "lib", "mix", "tasks"]))

    # Go strcase convert a module that end with digits to `<string>_<digit>` but 
    # Elixir convert back to `<string><digit>`. 
    mod_name = mod |> Macro.camelize() |> Macro.underscore()

    dot_formatter_exs = render_dot_formatter_exs()

    mix_exs =
      render_mix_exs(
        module: Macro.camelize(mod),
        application: atom(Macro.underscore(mod))
      )

    module = render_module(module: Macro.camelize(mod))

    File.write!(Path.join([mod, ".formatter.exs"]), dot_formatter_exs)
    File.write!(Path.join([mod, "mix.exs"]), mix_exs)
    File.write!(Path.join([mod, "lib", "#{mod_name}.ex"]), module)
  end

  defp atom(string), do: ":#{string}"

  @dot_formatter_exs """
  [
    import_deps: [:dagger],
    inputs: ["{mix,.formatter}.exs", "{config,lib,test}/**/*.{ex,exs}"]
  ]
  """

  EEx.function_from_string(:def, :render_dot_formatter_exs, @dot_formatter_exs, [])

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

    use Dagger.Mod.Object, name: "<%= @module %>"

    @doc \"\"\"
    Returns a container that echoes whatever string argument is provided.
    \"\"\"
    defn container_echo(string_arg: String.t()) :: Dagger.Container.t() do
      dag()
      |> Dagger.Client.container()
      |> Dagger.Container.from("alpine:latest")
      |> Dagger.Container.with_exec(~w"echo \#{string_arg}")
    end

    @doc \"\"\"
    Returns lines that match a pattern in the files of the provided Directory.
    \"\"\"
    defn grep_dir(directory_arg: Dagger.Directory.t(), pattern: String.t()) :: String.t() do
      dag()
      |> Dagger.Client.container()
      |> Dagger.Container.from("alpine:latest")
      |> Dagger.Container.with_mounted_directory("/mnt", directory_arg)
      |> Dagger.Container.with_workdir("/mnt")
      |> Dagger.Container.with_exec(["grep", "-R", pattern, "."])
      |> Dagger.Container.stdout()
    end
  end
  """

  EEx.function_from_string(:def, :render_module, @module_ex, [:assigns])
end

Main.run(System.argv())
