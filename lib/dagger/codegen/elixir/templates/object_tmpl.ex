defmodule Dagger.Codegen.Elixir.Templates.ObjectTmpl do
  @moduledoc false

  alias Dagger.Codegen.Elixir.Function

  def render_object(%{"name" => name, "fields" => fields, "description" => desc} = _full_type) do
    mod_name = Module.concat([Dagger, Function.format_module_name(name)])
    defs = render_functions(Function.format_name(name), fields)

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
    mod_var_name = Macro.var(mod_var_name, __MODULE__)
    fun_name = Function.format_name(name)
    deprecated = format_deprecated(field)
    doc = format_doc(field)
    fun = format_function(name, fun_name, {mod_var_name, args}, type_ref)

    body = [doc, fun]

    body =
      if not is_nil(deprecated) do
        [deprecated | body]
      else
        body
      end

    quote do
      (unquote_splicing(body))
    end
  end

  def format_function(
        field_name,
        fun_name,
        {mod_var_name, args},
        %{"kind" => "OBJECT", "name" => name}
      ) do
    mod_name = Module.concat([Dagger, Function.format_module_name(name)])
    args = render_args(args)
    fun_args = [module_fun_arg(mod_var_name) | fun_args(args)]

    body =
      quote do
        selection = select(unquote(mod_var_name).selection, unquote(field_name))

        unquote_splicing(args)

        %unquote(mod_name){
          selection: selection,
          client: unquote(mod_var_name).client
        }
      end

    Function.define(fun_name, fun_args, body)
  end

  def format_function(field_name, fun_name, {mod_var_name, args}, _) do
    args = render_args(args)
    fun_args = [module_fun_arg(mod_var_name) | fun_args(args)]

    body =
      quote do
        selection = select(unquote(mod_var_name).selection, unquote(field_name))

        unquote_splicing(args)

        execute(selection, unquote(mod_var_name).client)
      end

    Function.define(fun_name, fun_args, body)
  end

  defp format_deprecated(%{"isDeprecated" => true, "deprecationReason" => reason}) do
    quote do
      @deprecated unquote(reason)
    end
  end

  defp format_deprecated(_), do: nil

  defp format_doc(%{"description" => desc, "args" => args}) do
    required_args_doc = format_required_args_doc(args) |> IO.iodata_to_binary()
    optional_args_doc = format_optional_args_doc(args) |> IO.iodata_to_binary()

    doc =
      """
      #{desc}

      ## Required Arguments

      #{required_args_doc}

      ## Optional Arguments

      #{optional_args_doc}
      """
      |> String.trim()

    quote do
      @doc unquote(doc)
    end
  end

  defp format_required_args_doc(args) do
    args
    |> Enum.filter(&(&1["type"]["kind"] == "NON_NULL"))
    |> Enum.map(&format_arg_doc/1)
    |> Enum.intersperse('\n')
  end

  defp format_optional_args_doc(args) do
    args
    |> Enum.filter(&(&1["type"]["kind"] != "NON_NULL"))
    |> Enum.map(&format_arg_doc/1)
    |> Enum.intersperse('\n')
  end

  defp format_arg_doc(%{"name" => name, "description" => description}) do
    name = Function.format_name(name)
    "* `#{name}` - #{description}"
  end

  defp render_args(args) do
    required_args = render_required_args(args)
    optional_args = render_optional_args(args)

    if not is_nil(optional_args) do
      required_args ++ [optional_args]
    else
      required_args
    end
  end

  defp render_required_args(args) do
    for arg <- args,
        arg["type"]["kind"] == "NON_NULL" do
      name = Function.format_name(arg["name"])
      arg_name = to_string(name)

      quote do
        selection = arg(selection, unquote(arg_name), Keyword.fetch!(args, unquote(name)))
      end
    end
  end

  defp render_optional_args(args) do
    args =
      for arg <- args, arg["type"]["kind"] != "NON_NULL" do
        name = arg["name"]
        arg_name = Function.format_name(name)

        quote do
          selection =
            if not is_nil(args[unquote(arg_name)]) do
              arg(selection, unquote(name), args[unquote(arg_name)])
            else
              selection
            end
        end
      end

    case args do
      [] ->
        nil

      args ->
        quote do
          (unquote_splicing(args))
        end
    end
  end

  defp module_fun_arg(mod_var_name) do
    quote do
      %__MODULE__{} = unquote(mod_var_name)
    end
  end

  defp fun_args([]), do: []
  defp fun_args(_args), do: [Macro.var(:args, __MODULE__)]
end
