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
          "type" => %{"ofType" => type_ref},
          "description" => desc
        } = field
      ) do
    mod_var_name = Macro.var(mod_var_name, __MODULE__)
    fun_name = Function.format_name(name)
    deprecated = format_deprecated(field)
    doc = quote(do: @doc(unquote(desc)))
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
    fun_args = if(args == [], do: [], else: [Macro.var(:opts, __MODULE__)])
    args = render_args(args)

    quote do
      def unquote(fun_name)(%__MODULE__{} = unquote(mod_var_name), unquote_splicing(fun_args)) do
        selection = select(unquote(mod_var_name).selection, unquote(field_name))

        unquote_splicing(args)

        %unquote(mod_name){
          selection: selection,
          client: unquote(mod_var_name).client
        }
      end
    end
  end

  def format_function(field_name, fun_name, {mod_var_name, args}, _) do
    fun_args = if(args == [], do: [], else: [Macro.var(:opts, __MODULE__)])
    args = render_args(args)

    quote do
      def unquote(fun_name)(%__MODULE__{} = unquote(mod_var_name), unquote_splicing(fun_args)) do
        selection = select(unquote(mod_var_name).selection, unquote(field_name))

        unquote_splicing(args)

        execute(selection, unquote(mod_var_name).client)
      end
    end
  end

  defp format_deprecated(%{"isDeprecated" => true, "deprecationReason" => reason}) do
    quote do
      @deprecated unquote(reason)
    end
  end

  defp format_deprecated(_), do: nil

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

      quote do
        selection = arg(selection, to_string(unquote(name)), Keyword.fetch!(opts, unquote(name)))
      end
    end
  end

  defp render_optional_args(args) do
    args =
      for arg <- args,
          arg["type"]["kind"] != "NON_NULL" do
        Function.format_name(arg["name"])
      end

    case args do
      [] ->
        nil

      args ->
        quote do
          {_opts, selection} =
            unquote(args)
            |> Enum.reduce({opts, selection}, fn arg, {opts, selection} ->
              if not is_nil(opts[arg]) do
                {opts, arg(selection, to_string(arg), opts[arg])}
              else
                {opts, selection}
              end
            end)
        end
    end
  end
end
