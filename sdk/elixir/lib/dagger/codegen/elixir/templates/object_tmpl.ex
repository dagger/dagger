defmodule Dagger.Codegen.Elixir.Templates.Object do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Function
  alias Dagger.Codegen.Elixir.Module, as: Mod
  alias Dagger.Codegen.Elixir.Type

  # TODO: retire this and find a better way
  @id_modules [
    "CacheVolumeID",
    "ContainerID",
    "DirectoryID",
    "EnvVariableID",
    "FieldTypeDefID",
    "FileID",
    "FunctionArgID",
    "FunctionCallArgValueID",
    "FunctionCallID",
    "FunctionID",
    "GeneratedCodeID",
    "GitRefID",
    "GitRepositoryID",
    "HostID",
    "InterfaceTypeDefID",
    "LabelID",
    "ListTypeDefID",
    "ModuleConfigID",
    "ModuleID",
    "ObjectTypeDefID",
    "PortID",
    "SecretID",
    "ServiceID",
    "SocketID",
    "TypeDefID"
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
           "description" => desc
         },
         _
       )
       when name not in @id_modules do
    mod_name = Mod.from_name(name)

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
            type =
              case type do
                %{"kind" => "NON_NULL", "ofType" => type} -> Type.render_type(type)
                type -> Type.render_type(type) |> Type.render_nullable_type()
              end

            {to_struct_field(field), type}
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
           "description" => desc
         },
         types
       ) do
    mod_name = Mod.from_name(name)
    funs = Enum.map(fields, &render_function(&1, Function.format_var_name(name), types))

    derive_sync =
      if Enum.any?(fields, fn %{"name" => name} -> name == "sync" end) do
        [
          quote do
            @derive Dagger.Sync
          end
        ]
      else
        []
      end

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

        unquote_splicing(derive_sync)
        defstruct [:selection, :client]

        unquote_splicing(funs)
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
         {mod_var_name, [%{"name" => "id"} = arg]},
         type_ref,
         _types
       )
       when field_name in ["file", "secret"] do
    name =
      case type_ref do
        %{"kind" => "OBJECT", "name" => name} ->
          name

        %{"kind" => "NON_NULL", "ofType" => %{"kind" => "OBJECT", "name" => name}} ->
          name
      end

    mod_name = Mod.from_name(name)
    arg_name = arg |> fun_arg_name() |> Function.format_var_name()

    quote do
      selection = select(unquote(mod_var_name).selection, unquote(field_name))
      selection = arg(selection, "id", unquote(to_macro_var(arg_name)))

      %unquote(mod_name){
        selection: selection,
        client: unquote(mod_var_name).client
      }
    end
  end

  defp format_function_body(
         field_name,
         {mod_var_name, args},
         %{"kind" => "NON_NULL", "ofType" => %{"kind" => "OBJECT", "name" => name}},
         _types
       )
       when field_name != "moduleConfig" do
    name = if(name == "Query", do: "Client", else: name)
    mod_name = Mod.from_name(name)
    args = render_args(field_name, args)

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
    args = render_args(field_name, args)

    return_module = Mod.from_name(name)
    loader = "load#{name}FromID"

    # TODO(vito): technically we just need to select the ID, but selecting a
    # single field here seems to confuse the query builder. maybe pre-fetching
    # fields will come in useful anyway?
    selection_fields =
      types
      |> Enum.find(fn %{"name" => typename} -> typename == name end)
      |> then(fn %{"fields" => fields} -> get_in(fields, [Access.all(), "name"]) end)
      |> Enum.join(" ")

    quote do
      selection = select(unquote(mod_var_name).selection, unquote(field_name))
      selection = select(selection, unquote(selection_fields))

      unquote_splicing(args)

      with {:ok, data} <- execute(selection, unquote(mod_var_name).client) do
        {:ok,
         data
         |> Enum.map(fn value ->
           elem_selection = Dagger.QueryBuilder.Selection.query()
           elem_selection = select(elem_selection, unquote(loader))
           elem_selection = arg(elem_selection, "id", value["id"])

           %unquote(return_module){
             selection: elem_selection,
             client: unquote(mod_var_name).client
           }
         end)}
      end
    end
  end

  defp format_function_body(field_name, {mod_var_name, args}, type_ref, _types) do
    args = render_args(field_name, args)

    execute_block =
      case type_ref do
        %{"kind" => "OBJECT", "name" => name} ->
          return_module = Mod.from_name(name)

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

  defp render_args(field_name, args) do
    required_args = render_required_args(field_name, args)
    optional_args = render_optional_args(args)

    required_args ++ optional_args
  end

  defp render_required_args(field_name, args) do
    for arg <- args,
        arg["type"]["kind"] == "NON_NULL" do
      name = arg |> fun_arg_name() |> Function.format_var_name()

      basic =
        quote do
          selection = arg(selection, unquote(arg["name"]), unquote(to_macro_var(name)))
        end

      if String.starts_with?(field_name, "load") and String.ends_with?(field_name, "FromID") do
        # The point of loadFooFromID is to take an ID, so we don't want to
        # translate it to the real object.
        basic
      else
        case arg do
          %{"type" => %{"ofType" => %{"name" => type_name}}}
          when type_name in @id_modules ->
            mod = Mod.from_name(Mod.id_module_to_module(type_name))

            quote do
              {:ok, id} = unquote(mod).id(unquote(to_macro_var(name)))
              selection = arg(selection, unquote(arg["name"]), id)
            end

          _ ->
            basic
        end
      end
    end
  end

  defp render_optional_args(args) do
    for arg <- args, not required_arg?(arg) do
      name = arg["name"]
      arg_name = Function.format_var_name(name)

      render =
        case arg do
          %{
            "type" => %{
              "kind" => "LIST",
              "name" => nil,
              "ofType" => %{
                "kind" => "NON_NULL",
                "name" => nil,
                "ofType" => %{"kind" => "SCALAR", "name" => type_name, "ofType" => nil}
              }
            }
          }
          when type_name in @id_modules ->
            mod = Mod.from_name(Mod.id_module_to_module(type_name))

            quote do
              ids =
                optional_args[unquote(arg_name)]
                |> Enum.map(fn value ->
                  {:ok, id} = unquote(mod).id(value)
                  id
                end)

              arg(selection, unquote(name), ids)
            end

          %{
            "type" => %{"kind" => "SCALAR", "name" => type_name, "ofType" => nil}
          }
          when type_name in @id_modules ->
            mod = Mod.from_name(Mod.id_module_to_module(type_name))

            quote do
              {:ok, id} = unquote(mod).id(optional_args[unquote(arg_name)])
              arg(selection, unquote(name), id)
            end

          _ ->
            quote do
              arg(selection, unquote(name), optional_args[unquote(arg_name)])
            end
        end

      quote do
        selection =
          if is_nil(optional_args[unquote(arg_name)]) do
            selection
          else
            unquote(render)
          end
      end
    end
  end

  defp format_spec(%{"name" => name, "type" => type} = field) do
    return_type =
      case type do
        %{"kind" => "NON_NULL", "ofType" => %{"kind" => "OBJECT"} = type} ->
          Type.render_type(type)

        %{"kind" => "NON_NULL", "ofType" => type} ->
          Type.render_type(type)
          |> Type.render_result_type()

        type ->
          Type.render_type(type)
          |> Type.render_nullable_type()
          |> Type.render_result_type()
      end

    {[quote(do: t()) | render_arg_types(field["args"], name not in ["file", "secret"])],
     return_type}
  end

  defp render_arg_types(args, strip_id?) do
    {required_args, optional_args} =
      args
      |> Enum.split_with(&required_arg?/1)

    required_arg_types =
      for %{"type" => type} <- required_args do
        case type do
          %{"kind" => "NON_NULL", "ofType" => %{"kind" => "SCALAR"}} = type ->
            if strip_id? do
              update_in(type, ["ofType", "name"], fn name ->
                String.trim_trailing(name, "ID")
              end)
            else
              type
            end

          type ->
            type
        end
        |> Type.render_type()
      end

    if optional_args != [] do
      required_arg_types ++ [quote(do: keyword())]
    else
      required_arg_types
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
