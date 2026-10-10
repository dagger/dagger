defmodule Mix.Tasks.Dagger.Codegen do
  @shortdoc "Generate Dagger API from introspection.json"

  @moduledoc @shortdoc

  use Mix.Task

  alias Dagger.Codegen.Introspection.Types.Schema
  alias Dagger.Codegen.Naming

  def run(args) do
    :argparse.run(Enum.map(args, &String.to_charlist/1), cli(), %{progname: :dagger_codegen})
  end

  defp cli() do
    %{
      commands: %{
        ~c"generate" => %{
          arguments: [
            %{
              name: :outdir,
              type: :binary,
              long: ~c"-outdir",
              required: true
            },
            %{
              name: :introspection,
              type: :binary,
              long: ~c"-introspection",
              required: true
            },
            # The schema's names as the engine formatted them, from
            # `codegen introspect --names-out` (see Dagger.Codegen.Naming).
            %{
              name: :names,
              type: :binary,
              long: ~c"-names",
              required: false
            }
          ],
          handler: &handle_generate/1
        }
      }
    }
  end

  def handle_generate(%{outdir: outdir, introspection: introspection} = args) do
    schema = introspection |> File.read!() |> JSON.decode!()

    names =
      case args do
        %{names: path} -> path |> File.read!() |> JSON.decode!() |> Naming.from_map()
        _ -> nil
      end

    IO.puts("Generate code to #{outdir}")

    File.mkdir_p!(outdir)

    Dagger.Codegen.generate(
      Dagger.Codegen.ElixirGenerator,
      schema |> Schema.from_map() |> Schema.put_names(names)
    )
    |> Task.async_stream(
      fn {:ok, {file, code}} ->
        Path.join(outdir, file)
        |> File.write!(code)
      end,
      ordered: false,
      timeout: :infinity
    )
    |> Stream.run()
  end
end
