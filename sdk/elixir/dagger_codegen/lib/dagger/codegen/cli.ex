defmodule Dagger.Codegen.CLI do
  @moduledoc """
  Main entrypoint for Dagger codegen binary.
  """

  def main(args) do
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
            }
          ],
          handler: &handle_generate/1
        }
      }
    }
  end

  def handle_generate(%{outdir: outdir, introspection: introspection}) do
    %{"__schema" => schema} = introspection |> File.read!() |> Jason.decode!()

    IO.puts("Generate code to #{Path.expand(outdir)}")

    File.mkdir_p!(outdir)

    Dagger.Codegen.generate(
      Dagger.Codegen.ElixirGenerator,
      Dagger.Codegen.Introspection.Types.Schema.from_map(schema)
    )
    |> Enum.flat_map(& &1)
    |> Enum.each(fn {file, code} ->
      Path.join(outdir, file)
      |> File.write!(code)
    end)
  end
end
