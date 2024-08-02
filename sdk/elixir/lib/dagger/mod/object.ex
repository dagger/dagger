defmodule Dagger.Mod.Object do
  @moduledoc """
  Declare a module as an object type.
  """

  # TODO: defn handle ignore argument correctly.
  # TODO: support optional

  @type function_name() :: atom()
  @type function_def() :: {function_name(), keyword()}

  alias Dagger.Mod.Object.Defn

  @doc """
  Get module documentation.

  Returns module doc string or `nil` if the given module didn't have a documentation.
  """
  @spec get_module_doc(module()) :: String.t() | nil
  def get_module_doc(module) do
    with {:docs_v1, _, :elixir, _, module_doc, _, _} <- Code.fetch_docs(module),
         %{"en" => doc} <- module_doc do
      String.trim(doc)
    else
      :none -> nil
      :hidden -> nil
      {:error, :module_not_found} -> nil
    end
  end

  @doc """
  List all available function definitions from the given module.
  """
  @spec list_functions(module()) :: [function_def()]
  def list_functions(module) do
    module.__info__(:attributes)
    |> Keyword.get_values(:function)
    |> Enum.flat_map(& &1)
  end

  @doc """
  Get function definition from the given module.
  """
  @spec get_function(module(), function_name()) :: function_def()
  def get_function(module, name) when is_atom(name) do
    list_functions(module)
    |> Keyword.fetch!(name)
  end

  @doc """
  Get function documentation.

  Return doc string or `nil` if that function didn't have a documentation.
  """
  @spec get_function_doc(module(), function_name()) :: String.t() | nil
  def get_function_doc(module, name) do
    fun = fn
      {{:function, ^name, _}, _, _, _, _} -> true
      _ -> false
    end

    with {:docs_v1, _, :elixir, _, _, _, function_docs} <- Code.fetch_docs(module),
         {{:function, ^name, _}, _, _, doc_content, _} <- Enum.find(function_docs, fun),
         %{"en" => doc} <- doc_content do
      String.trim(doc)
    else
      nil -> nil
      :none -> nil
      :hidden -> nil
    end
  end

  @doc """
  Get object name from the given module.
  """
  @spec get_name(module()) :: String.t()
  def get_name(module) do
    module.__info__(:attributes)
    |> Keyword.fetch!(:name)
    |> to_string()
  end

  defmacro __using__(opts) do
    quote do
      use Dagger.Mod, unquote(opts)

      import Dagger.Mod.Object, only: [defn: 2]
      import Dagger.Global, only: [dag: 0]

      Module.register_attribute(__MODULE__, :function, accumulate: true, persist: true)
    end
  end

  @doc """
  Declare a function.
  """
  defmacro defn(call, do: block) do
    {name, args, return} = extract_call(call)
    has_self? = is_tuple(args)
    arg_defs = compile_args(args)
    return_def = compile_typespec!(return)

    quote do
      @function {unquote(name),
                 [self: unquote(has_self?), args: unquote(arg_defs), return: unquote(return_def)]}
      unquote(Defn.define(name, args, return, block))
    end
  end

  defp extract_call({:"::", _, [call_def, return]}) do
    {name, args} = extract_call_def(call_def)
    {name, args, return}
  end

  defp extract_call_def({name, _, []}) do
    {name, []}
  end

  defp extract_call_def({name, _, [args]}) do
    {name, args}
  end

  defp extract_call_def({name, _, [self, args]}) do
    {name, {self, args}}
  end

  defp compile_args({_, args}) do
    compile_args(args)
  end

  defp compile_args(args) do
    for {name, spec} <- args do
      {name, [type: compile_typespec!(spec)]}
    end
  end

  # binary()
  defp compile_typespec!({:binary, _, []}), do: :string
  # integer()
  defp compile_typespec!({:integer, _, []}), do: :integer
  # boolean()
  defp compile_typespec!({:boolean, _, []}), do: :boolean

  # String.t() 
  defp compile_typespec!(
         {{:., _,
           [
             {:__aliases__, _, [:String]},
             :t
           ]}, _, []}
       ) do
    :string
  end

  defp compile_typespec!({{:., _, [{:__aliases__, _, module}, :t]}, _, []}) do
    Module.concat(module)
  end

  defp compile_typespec!({:list, _, [type]}) do
    {:list, compile_typespec!(type)}
  end

  defp compile_typespec!([type]) do
    {:list, compile_typespec!(type)}
  end

  defp compile_typespec!(unsupported_type) do
    raise ArgumentError, "type `#{Macro.to_string(unsupported_type)}` is not supported"
  end
end
