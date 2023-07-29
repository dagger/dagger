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

  def render(full_type, types) do
    if is_simple_object?(full_type) do
      render_data_module(full_type, types)
    else
      render_api_module(full_type, types)
    end
  end

  defp is_simple_object?(%{"fields" => fields}) do
    not has_id_fields?(fields) and empty_args?(fields)
  end

  defp has_id_fields?(fields) do
    fields
    |> Enum.any?(fn %{"name" => name} -> name == "id" end)
  end

  defp empty_args?(fields) do
    fields
    |> Enum.all?(fn %{"args" => args} -> args == [] end)
  end

  defp render_data_module(
         %{
           "name" => name,
           "fields" => fields,
           "description" => desc,
           "private" => %{mod_name: mod_name}
         },
         _
       )
       when name not in @id_modules do
    desc =
      if desc == "" do
        name
      else
        desc
      end

    struct_fields = Enum.map(fields, &to_struct_field/1)

    data_type_t =
      {:%, [],
       [
         {:__MODULE__, [], Elixir},
         {:%{}, [],
          fields
          |> Enum.map(fn %{"type" => type} = field ->
            {to_struct_field(field), render_return_type(type)}
          end)}
       ]}

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)

        @type t() :: unquote(data_type_t)

        @derive Nestru.Decoder
        defstruct unquote(struct_fields)
      end
    end
  end

  defp to_struct_field(%{"name" => name}) do
    name
    |> Macro.underscore()
    |> String.to_atom()
  end

  defp render_api_module(
         %{
           "name" => name,
           "fields" => fields,
           "description" => desc,
           "private" => %{mod_name: mod_name}
         },
         types
       ) do
    defs = Enum.map(fields, &render_function(&1, Function.format_var_name(name), types))

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

  defp render_function(
         %{
           "name" => name,
           "args" => args,
           "type" => type
         } = field,
         mod_var_name,
         types
       ) do
    mod_var_name = to_macro_var(mod_var_name)
    fun_args = [module_fun_arg(mod_var_name) | fun_args(args)]
    fun_body = format_function_body(name, {mod_var_name, args}, type, types)

    Function.define(name, fun_args, nil, fun_body,
      doc: format_doc(field),
      deprecated: deprecated_reason(field),
      spec: format_spec(field)
    )
  end

  defp format_function_body(
         field_name,
         {mod_var_name, args},
         %{"kind" => "NON_NULL", "ofType" => %{"kind" => "OBJECT", "name" => name}},
         _types
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

  defp format_function_body(
         field_name,
         {mod_var_name, args},
         %{
           "kind" => "NON_NULL",
           "ofType" => %{
             "kind" => "LIST",
             "ofType" => %{"ofType" => %{"kind" => "OBJECT", "name" => name}}
           }
         },
         types
       ) do
    args = render_args(args)

    selection_fields =
      types
      |> Enum.find(fn %{"name" => typename} -> typename == name end)
      |> then(fn %{"fields" => fields} -> get_in(fields, [Access.all(), "name"]) end)
      |> Enum.join(" ")

    return_module = Module.concat([Dagger, Mod.format_name(name)])

    quote do
      selection = select(unquote(mod_var_name).selection, unquote(field_name))
      selection = select(selection, unquote(selection_fields))

      unquote_splicing(args)

      with {:ok, data} <- execute(selection, unquote(mod_var_name).client) do
        Nestru.decode_from_list_of_maps(data, unquote(return_module))
      end
    end
  end

  defp format_function_body(field_name, {mod_var_name, args}, type_ref, _types) do
    args = render_args(args)

    execute_block =
      case type_ref do
        %{"kind" => "OBJECT", "name" => name} ->
          return_module = Module.concat([Dagger, Mod.format_name(name)])

          quote do
            case execute(selection, unquote(mod_var_name).client) do
              {:ok, nil} ->
                {:ok, nil}

              {:ok, data} ->
                Nestru.decode_from_map(data, unquote(return_module))

              error ->
                error
            end
          end

        _ ->
          quote do
            execute(selection, unquote(mod_var_name).client)
          end
      end

    quote do
      selection = select(unquote(mod_var_name).selection, unquote(field_name))

      unquote_splicing(args)

      unquote(execute_block)
    end
  end

  defp deprecated_reason(%{"isDeprecated" => true, "deprecationReason" => reason}) do
    reason = String.trim_trailing(reason, ".")

    for [text, api] <- Regex.scan(~r/`(?<name>[a-zA-Z0-9]+)`/, reason),
        reduce: reason do
      reason -> String.replace(reason, text, "`#{Macro.underscore(api)}`")
    end
  end

  defp deprecated_reason(_), do: nil

  defp format_doc(%{"description" => desc, "args" => args}) do
    sep = [~c"\n", ~c"\n"]
    doc = [desc]

    required_args_doc =
      case format_required_args_doc(args) do
        [] -> []
        args_doc -> ["## Required Arguments", ~c"\n", ~c"\n", args_doc]
      end

    optional_args_doc =
      case format_optional_args_doc(args) do
        [] -> []
        args_doc -> ["## Optional Arguments", ~c"\n", ~c"\n", args_doc]
      end

    (doc ++ sep ++ required_args_doc ++ sep ++ optional_args_doc)
    |> IO.iodata_to_binary()
    |> String.trim()
  end

  defp format_required_args_doc(args) do
    args
    |> Enum.filter(&required_arg?/1)
    |> Enum.map(&format_arg_doc/1)
    |> Enum.intersperse(~c"\n")
  end

  defp format_optional_args_doc(args) do
    args
    |> Enum.filter(&(not required_arg?(&1)))
    |> Enum.map(&format_arg_doc/1)
    |> Enum.intersperse(~c"\n")
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
            {:ok, id} = unquote(mod).id(unquote(to_macro_var(name)))
            selection = arg(selection, unquote(arg["name"]), id)
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
    render_type(type)
  end

  defp render_return_type(type) do
    {:|, [], [render_type(type), nil]}
  end

  defp render_type(%{"kind" => "OBJECT", "name" => type}) do
    mod_name = Module.concat([Dagger, Mod.format_name(type)])

    quote do
      unquote(mod_name).t()
    end
  end

  defp render_type(%{"kind" => "NON_NULL", "ofType" => type}) do
    render_type(type)
  end

  defp render_type(%{"kind" => "LIST", "ofType" => type}) do
    type = render_type(type)

    quote do
      [unquote(type)]
    end
  end

  defp render_type(%{"kind" => "ENUM", "name" => type}) do
    mod_name = Module.concat([Dagger, Mod.format_name(type)])

    quote do
      unquote(mod_name).t()
    end
  end

  defp render_type(%{"kind" => "SCALAR", "name" => "String"}) do
    quote do
      String.t()
    end
  end

  defp render_type(%{"kind" => "SCALAR", "name" => "Int"}) do
    quote do
      integer()
    end
  end

  defp render_type(%{"kind" => "SCALAR", "name" => "Float"}) do
    quote do
      float()
    end
  end

  defp render_type(%{"kind" => "SCALAR", "name" => "Boolean"}) do
    quote do
      boolean()
    end
  end

  defp render_type(%{"kind" => "SCALAR", "name" => type}) do
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
