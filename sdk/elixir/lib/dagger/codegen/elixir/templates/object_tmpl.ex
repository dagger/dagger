defmodule Dagger.Codegen.Elixir.Templates.Object do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Function
  alias Dagger.Codegen.Elixir.Module, as: Mod

  @id_modules [
    "CacheID",
    "ContainerID",
    "DirectoryID",
    "FileID",
    "ProjectCommandID",
    "ProjectID",
    "SecretID",
    "SocketID"
  ]

  def render(
        %{
          "name" => name,
          "fields" => fields,
          "description" => desc,
          "private" => %{mod_name: mod_name}
        } = _full_type
      ) do
    defs = render_functions(Function.format_var_name(name), fields)

    desc =
      if desc == "" do
        name
      else
        desc
      end

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)

        use Dagger.QueryBuilder

        @type t() :: %__MODULE__{}

        defstruct [:selection, :client]

        unquote_splicing(defs)
      end
    end
  end

  def render_functions(mod_var_name, fields) do
    for field <- fields do
      render_function(mod_var_name, field)
    end
  end

  def render_function(
        mod_var_name,
        %{
          "name" => name,
          "args" => args,
          "type" => %{"ofType" => type_ref}
        } = field
      ) do
    mod_var_name = to_macro_var(mod_var_name)
    fun_args = [module_fun_arg(mod_var_name) | fun_args(args)]
    fun_body = format_function_body(name, {mod_var_name, args}, type_ref)

    Function.define(name, fun_args, nil, fun_body,
      doc: format_doc(field),
      deprecated: deprecated_reason(field),
      spec: format_spec(field)
    )
  end

  def format_function_body(
        field_name,
        {mod_var_name, args},
        %{"kind" => "OBJECT", "name" => name}
      ) do
    mod_name = Module.concat([Dagger, Mod.format_name(name)])
    args = render_args(args)

    quote do
      selection = select(unquote(mod_var_name).selection, unquote(field_name))

      unquote_splicing(args)

      %unquote(mod_name){
        selection: selection,
        client: unquote(mod_var_name).client
      }
    end
  end

  def format_function_body(field_name, {mod_var_name, args}, _) do
    args = render_args(args)

    quote do
      selection = select(unquote(mod_var_name).selection, unquote(field_name))

      unquote_splicing(args)

      execute(selection, unquote(mod_var_name).client)
    end
  end

  defp deprecated_reason(%{"isDeprecated" => true, "deprecationReason" => reason}) do
    String.trim_trailing(reason, ".")
  end

  defp deprecated_reason(_), do: nil

  defp format_doc(%{"description" => desc, "args" => args}) do
    sep = ['\n', '\n']
    doc = [desc]

    required_args_doc =
      case format_required_args_doc(args) do
        [] -> []
        args_doc -> ["## Required Arguments", '\n', '\n', args_doc]
      end

    optional_args_doc =
      case format_optional_args_doc(args) do
        [] -> []
        args_doc -> ["## Optional Arguments", '\n', '\n', args_doc]
      end

    (doc ++ sep ++ required_args_doc ++ sep ++ optional_args_doc)
    |> IO.iodata_to_binary()
    |> String.trim()
  end

  defp format_required_args_doc(args) do
    args
    |> Enum.filter(&required_arg?/1)
    |> Enum.map(&format_arg_doc/1)
    |> Enum.intersperse('\n')
  end

  defp format_optional_args_doc(args) do
    args
    |> Enum.filter(&(not required_arg?(&1)))
    |> Enum.map(&format_arg_doc/1)
    |> Enum.intersperse('\n')
  end

  defp required_arg?(arg) do
    arg["type"]["kind"] == "NON_NULL"
  end

  defp format_arg_doc(%{"name" => name, "description" => description}) do
    name = Function.format_var_name(name)
    "* `#{name}` - #{description}"
  end

  defp render_args(args) do
    required_args = render_required_args(args)
    optional_args = render_optional_args(args)

    required_args ++ optional_args
  end

  defp render_required_args(args) do
    for arg <- args,
        arg["type"]["kind"] == "NON_NULL" do
      name = arg |> fun_arg_name() |> Function.format_var_name()

      case arg do
        %{"type" => %{"ofType" => %{"name" => type_name}}}
        when type_name in @id_modules ->
          mod = Module.concat([Dagger, Mod.format_name(Mod.id_module_to_module(type_name))])

          quote do
            selection =
              arg(
                selection,
                unquote(arg["name"]),
                unquote(mod).id(unquote(to_macro_var(name)))
              )
          end

        _ ->
          quote do
            selection = arg(selection, unquote(arg["name"]), unquote(to_macro_var(name)))
          end
      end
    end
  end

  defp render_optional_args(args) do
    for arg <- args, not required_arg?(arg) do
      name = arg["name"]
      arg_name = Function.format_var_name(name)

      quote do
        selection =
          if is_nil(optional_args[unquote(arg_name)]) do
            selection
          else
            arg(selection, unquote(name), optional_args[unquote(arg_name)])
          end
      end
    end
  end

  defp format_spec(field) do
    {[quote(do: t()) | render_arg_types(field["args"])], render_return_type(field["type"])}
  end

  defp render_arg_types(args) do
    {required_args, optional_args} =
      args
      |> Enum.split_with(&required_arg?/1)

    required_arg_types =
      for %{"type" => type} <- required_args do
        render_return_type(type)
      end

    if optional_args != [] do
      required_arg_types ++ [quote(do: keyword())]
    else
      required_arg_types
    end
  end

  defp render_return_type(%{"kind" => "NON_NULL", "ofType" => type}) do
    render_return_type(type)
  end

  defp render_return_type(%{"kind" => "OBJECT", "name" => type}) do
    mod_name = Module.concat([Dagger, Mod.format_name(type)])

    quote do
      unquote(mod_name).t()
    end
  end

  defp render_return_type(%{"kind" => "LIST", "ofType" => type}) do
    type = render_return_type(type)

    quote do
      [unquote(type)]
    end
  end

  defp render_return_type(%{"kind" => "ENUM", "name" => type}) do
    mod_name = Module.concat([Dagger, Mod.format_name(type)])

    quote do
      unquote(mod_name).t()
    end
  end

  defp render_return_type(%{"kind" => "SCALAR", "name" => "String"}) do
    quote do
      String.t()
    end
  end

  defp render_return_type(%{"kind" => "SCALAR", "name" => "Int"}) do
    quote do
      integer()
    end
  end

  defp render_return_type(%{"kind" => "SCALAR", "name" => "Float"}) do
    quote do
      float()
    end
  end

  defp render_return_type(%{"kind" => "SCALAR", "name" => "Boolean"}) do
    quote do
      boolean()
    end
  end

  defp render_return_type(%{"kind" => "SCALAR", "name" => type}) do
    # Convert *ID type into object type.
    mod_name = Module.concat([Dagger, type |> String.trim_trailing("ID") |> Mod.format_name()])

    quote do
      unquote(mod_name).t()
    end
  end

  defp module_fun_arg(mod_var_name) do
    quote do
      %__MODULE__{} = unquote(mod_var_name)
    end
  end

  defp fun_args([]), do: []

  defp fun_args(args) do
    {required_args, optional_args} =
      args
      |> Enum.split_with(&required_arg?/1)

    required_fun_args(required_args) ++ optional_fun_args(optional_args)
  end

  defp required_fun_args(args) do
    args
    |> Enum.map(&Function.format_var_name(fun_arg_name(&1)))
    |> Enum.map(&to_macro_var/1)
  end

  defp optional_fun_args([]), do: []

  defp optional_fun_args(_args) do
    [
      quote do
        unquote(to_macro_var(:optional_args)) \\ []
      end
    ]
  end

  defp fun_arg_name(%{"name" => "id", "type" => %{"ofType" => %{"name" => id_mod}}})
       when id_mod in @id_modules do
    Function.id_module_to_var_name(id_mod)
  end

  defp fun_arg_name(%{"name" => name}) do
    name
  end

  defp to_macro_var(var), do: Macro.var(var, __MODULE__)
end
