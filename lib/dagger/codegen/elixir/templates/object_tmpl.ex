defmodule Dagger.Codegen.Elixir.Templates.ObjectTmpl do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Function

  def render_object(%{"name" => name, "fields" => fields, "description" => desc} = _full_type) do
    mod_name = Module.concat([Dagger, Function.format_module_name(name)])
    defs = render_functions(Function.format_var_name(name), fields)

    quote do
      defmodule unquote(mod_name) do
        @moduledoc unquote(desc)

        use Dagger.QueryBuilder

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
      deprecated: deprecated_reason(field)
    )
  end

  def format_function_body(
        field_name,
        {mod_var_name, args},
        %{"kind" => "OBJECT", "name" => name}
      ) do
    mod_name = Module.concat([Dagger, Function.format_module_name(name)])
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
    reason
  end

  defp deprecated_reason(_), do: nil

  defp format_doc(%{"description" => desc, "args" => args}) do
    required_args_doc = format_required_args_doc(args) |> IO.iodata_to_binary()
    optional_args_doc = format_optional_args_doc(args) |> IO.iodata_to_binary()

    """
    #{desc}

    ## Required Arguments

    #{required_args_doc}

    ## Optional Arguments

    #{optional_args_doc}
    """
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
      name = Function.format_var_name(fun_arg_name(arg))

      quote do
        selection = arg(selection, unquote(arg["name"]), unquote(to_macro_var(name)))
      end
    end
  end

  defp render_optional_args(args) do
    for arg <- args, not required_arg?(arg) do
      name = arg["name"]
      arg_name = Function.format_var_name(name)

      quote do
        selection =
          if not is_nil(optional_args[unquote(arg_name)]) do
            arg(selection, unquote(name), optional_args[unquote(arg_name)])
          else
            selection
          end
      end
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

  # Field name id has no meaning since we don't have a typespec. Use
  # type name to argument name instead.
  defp fun_arg_name(%{
         "name" => "id",
         "type" => %{kind: "NON_NULL", ofType: %{"name" => type_name}}
       }) do
    type_name
  end

  defp fun_arg_name(%{"name" => name}) do
    name
  end

  defp to_macro_var(var), do: Macro.var(var, __MODULE__)
end
