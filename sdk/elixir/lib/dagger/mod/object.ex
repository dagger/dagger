defmodule Dagger.Mod.Object do
  @moduledoc """
  Declare a module as an object type.

  ## Declare an object

  Add `use Dagger.Mod.Object` to the Elixir module that want to be a
  Dagger module and give a name through `:name` configuration:

      defmodule Potato do
        use Dagger.Mod.Object, name: "Potato"

        # ...
      end

  The module also support documentation by using Elixir standard documentation,
  `@moduledoc`.

  ## Declare a function

  The module provides a `defn`, a macro for declare a function.
  Let's declare a new function named `echo` that accepts a `name` as a string
  and return a container that echo a name in the module `Potato` from the previous
  section:

      defmodule Potato do
        use Dagger.Mod.Object, name: "Potato"

        defn echo(name: String.t()) :: Dagger.Container.t() do
          dag()
          |> Dagger.Client.container()
          |> Dagger.Container.from("alpine")
          |> Dagger.Container.with_exec(["echo", name])
        end
      end

  From the example above, the `defn` allows you to annotate a type to function
  arguments and return type by using Elixir Typespec. The type will convert to
  a Dagger type when registering a module.

  The supported primitive types for now are:

  1. `integer()` for a boolean type.
  2. `boolean()` for a boolean type.
  3. `String.t()` or `binary()` for a string type.
  4. `list(type)` or `[type]` for a list type.
  5. `type | nil` for optional type.
  6. Any type that generated under `Dagger` namespace (`Dagger.Container.t()`,
     `Dagger.Directory.t()`, etc.).

  The function also support documentation by using Elixir standard documentation,
  `@doc`.
  """

  @type function_name() :: atom()
  @type function_def() :: {function_name(), keyword()}

  alias Dagger.Mod.Object.Defn
  alias Dagger.Mod.Object.Meta

  @doc """
  Get module documentation.

  Returns module doc string or `nil` if the given module didn't have a documentation.
  """
  @spec get_module_doc(module()) :: String.t() | nil
  def get_module_doc(module) do
    with {module_doc, _} <- fetch_docs(module),
         %{"en" => doc} <- module_doc do
      String.trim(doc)
    else
      :none -> nil
      :hidden -> nil
      {:error, :module_not_found} -> nil
    end
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

    with {_, function_docs} <- fetch_docs(module),
         {{:function, ^name, _}, _, _, doc_content, _} <- Enum.find(function_docs, fun),
         %{"en" => doc} <- doc_content do
      String.trim(doc)
    else
      nil -> nil
      :none -> nil
      :hidden -> nil
    end
  end

  defp fetch_docs(module) do
    {:docs_v1, _, :elixir, _, module_doc, _, function_docs} = Code.fetch_docs(module)
    {module_doc, function_docs}
  end

  defmacro __using__(opts) do
    name = opts[:name]

    quote do
      import Dagger.Mod.Object, only: [defn: 2]
      import Dagger.Global, only: [dag: 0]

      Module.register_attribute(__MODULE__, :function, accumulate: true, persist: true)

      # Get an object name
      def __object__(:name), do: unquote(name)

      # List available function definitions.
      def __object__(:functions) do
        __MODULE__.__info__(:attributes)
        |> Keyword.get_values(:function)
        |> Enum.flat_map(& &1)
      end

      # Get a function definition.
      def __object__(:function, name) do
        __object__(:functions)
        |> Keyword.fetch!(name)
      end
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
      type = compile_typespec!(spec)
      meta = spec |> extract_options() |> Keyword.put(:type, type)
      {name, Meta.validate!(meta)}
    end
  end

  defp compile_typespec!({:integer, _, []}), do: :integer
  defp compile_typespec!({:boolean, _, []}), do: :boolean

  ## String

  defp compile_typespec!({:binary, _, []}), do: :string

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

  ## List

  defp compile_typespec!({:list, _, [type]}) do
    {:list, compile_typespec!(type)}
  end

  defp compile_typespec!([type]) do
    {:list, compile_typespec!(type)}
  end

  ## Optional

  defp compile_typespec!({:|, _, [type, nil]}) do
    {:optional, compile_typespec!(type)}
  end

  ## Type with options

  defp compile_typespec!({type, _}) do
    compile_typespec!(type)
  end

  defp compile_typespec!(unsupported_type) do
    raise ArgumentError, "type `#{Macro.to_string(unsupported_type)}` is not supported"
  end

  defp extract_options({_, options}), do: options
  defp extract_options(_), do: []
end
