defmodule Dagger.Codegen.Elixir.Function do
  @moduledoc false

  @id_modules_map %{
    "CacheVolumeID" => "cache_volume",
    "ContainerID" => "container",
    "DirectoryID" => "directory",
    "FileID" => "file",
    "ProjectCommandID" => "project_command",
    "ProjectID" => "project",
    "SecretID" => "secret",
    "SocketID" => "socket",
    "ServiceID" => "service"
  }

  def id_module_to_var_name(id_mod), do: Map.fetch!(@id_modules_map, id_mod)

  def format_var_name(name) when is_binary(name) do
    format_name(name)
  end

  defp format_fun_name(name) when is_atom(name) do
    name
    |> to_string()
    |> format_fun_name()
  end

  defp format_fun_name(name) when is_binary(name) do
    format_name(name)
  end

  defp format_name(name) when is_binary(name) do
    name
    |> Macro.underscore()
    |> String.to_atom()
  end

  def define(fun_name, args, guard \\ nil, body, opts \\ []) when is_list(args) do
    fun_name = format_fun_name(fun_name)
    doc = opts[:doc] |> doc_to_quote()
    deprecated = opts[:deprecated] |> deprecated_to_quote()
    typespec = opts[:spec] |> spec_to_quote(fun_name)

    fun = [
      case guard do
        nil ->
          quote do
            def unquote(fun_name)(unquote_splicing(args)) do
              unquote(body)
            end
          end

        guard ->
          quote do
            def unquote(fun_name)(unquote_splicing(args)) when unquote(guard) do
              unquote(body)
            end
          end
      end
    ]

    quote do
      (unquote_splicing(doc ++ deprecated ++ typespec ++ fun))
    end
  end

  defp doc_to_quote(nil) do
    [
      quote do
        @doc false
      end
    ]
  end

  defp doc_to_quote(doc) when is_binary(doc) do
    [
      quote do
        @doc unquote(doc)
      end
    ]
  end

  defp deprecated_to_quote(nil), do: []

  defp deprecated_to_quote(deprecated) do
    [
      quote do
        @deprecated unquote(deprecated)
      end
    ]
  end

  defp spec_to_quote(nil, _), do: []

  defp spec_to_quote({arg_types, return_type}, fun_name) do
    [
      quote do
        @spec unquote(fun_name)(unquote_splicing(arg_types)) :: unquote(return_type)
      end
    ]
  end
end
