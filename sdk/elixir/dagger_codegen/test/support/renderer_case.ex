defmodule Dagger.Codegen.RendererCase do
  use ExUnit.CaseTemplate

  using do
    quote do
      use Mneme
      import Dagger.Codegen.RendererCase, only: [render_type: 2]
    end
  end

  defp decode_type_from_file(path) do
    path
    |> File.read!()
    |> :json.decode()
    |> Dagger.Codegen.Introspection.Types.Type.from_map()
  end

  defp render(type, renderer) do
    renderer.render(type)
    |> IO.iodata_to_binary()
    |> Code.format_string!()
    |> IO.iodata_to_binary()
  end

  def render_type(renderer, path) do
    path
    |> decode_type_from_file()
    |> render(renderer)
  end
end
