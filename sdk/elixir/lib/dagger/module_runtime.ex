defmodule Dagger.ModuleRuntime do
  @schema [
            args: [
              doc: """
              Arguments of the function.

              Everything declared in this keyword will pass into the second argument
              of the function as a `map`.
              """,
              type: :keyword_list,
              required: true,
              keys: [
                *: [
                  type: :non_empty_keyword_list,
                  required: true,
                  keys: [
                    type: [
                      doc: """
                      Type of the argument.

                      The possible values are:

                      * `:boolean` - A boolean type.
                      * `:integer` - A integer type.
                      * `:string` - A string type.
                      * `{:list, type}` - A list of `type`.
                      * `module` - An Elixir module.
                      """,
                      type:
                        {:or,
                         [
                           :atom,
                           {:tuple, [:atom, :atom]}
                         ]},
                      required: true
                    ]
                  ]
                ]
              ]
            ],
            return: [
              doc: """
              Functionre\tbturn type.

              The possible values are:

              * `:boolean` - A boolean type.
              * `:integer` - A integer type.
              * `:string` - A string type.
              * `{:list, type}` - A list of `type`.
              * `module` - An Elixir module.
              """,
              type:
                {:or,
                 [
                   :atom,
                   {:tuple, [:atom, :atom]}
                 ]},
              required: true
            ]
          ]
          |> NimbleOptions.new!()

  @moduledoc """
  `Dagger.ModuleRuntime` is a runtime for `Dagger` module for Elixir.

  ## Function schema

  #{NimbleOptions.docs(@schema)}
  """

  def __on_definition__(env, :def, name, args, _guards, _body) do
    case Module.get_attribute(env.module, :function) do
      nil ->
        :ok

      function ->
        unless length(args) == 2 do
          raise """
          A function must have 2 arguments.
          """
        end

        function = NimbleOptions.validate!(function, @schema)
        functions = Module.get_attribute(env.module, :functions)
        functions = [{name, function} | functions]
        Module.put_attribute(env.module, :functions, functions)
        Module.delete_attribute(env.module, :function)
    end
  end

  def __on_definition__(env, :defp, name, _args, _guards, _body) do
    case Module.get_attribute(env.module, :function) do
      nil ->
        :ok

      _ ->
        raise """
        Define `@function` on private function (#{name}) is not supported.
        """
    end
  end

  def function_schema(), do: @schema

  @doc """
  Invoke a function.
  """
  def invoke(dag \\ Dagger.connect!()) do
    fn_call = Dagger.Client.current_function_call(dag)

    with {:ok, parent_name} <- Dagger.FunctionCall.parent_name(fn_call),
         {:ok, fn_name} <- Dagger.FunctionCall.name(fn_call),
         {:ok, parent_json} <- Dagger.FunctionCall.parent(fn_call),
         {:ok, parent} <- Jason.decode(parent_json),
         {:ok, input_args} <- Dagger.FunctionCall.input_args(fn_call),
         {:ok, json} <- invoke(dag, parent, parent_name, fn_name, input_args),
         {:ok, _} <- Dagger.FunctionCall.return_value(fn_call, json) do
      :ok
    else
      {:error, reason} ->
        IO.puts(inspect(reason))
        System.halt(2)
    end
  after
    Dagger.close(dag)
  end

  def invoke(dag, _parent, "", _fn_name, _input_args) do
    # TODO: find the way on how to register multiple modules.
    [module] = Dagger.ModuleRuntime.Registry.all()

    dag
    |> Dagger.ModuleRuntime.Module.define(module)
    |> encode(Dagger.Module)
  end

  def invoke(dag, _parent, parent_name, fn_name, input_args) do
    case Dagger.ModuleRuntime.Registry.get(parent_name) do
      nil ->
        {:error,
         "unknown module #{parent_name}, please make sure the module is created and register to supervision tree in the application."}

      module ->
        fun = fn_name |> Macro.underscore() |> String.to_existing_atom()
        fun_def = Dagger.ModuleRuntime.Module.get_function_definition(module, fun)
        args = decode_args(dag, input_args, Keyword.fetch!(fun_def, :args))
        return_type = Keyword.fetch!(fun_def, :return)

        case apply(module, fun, [struct(module, dag: dag), args]) do
          {:error, _} = error -> error
          {:ok, result} -> encode(result, return_type)
          result -> encode(result, return_type)
        end
    end
  end

  def decode_args(dag, input_args, args_def) do
    Enum.into(input_args, %{}, fn arg ->
      with {:ok, name} = Dagger.FunctionCallArgValue.name(arg),
           name = String.to_existing_atom(name),
           {:ok, value} <- Dagger.FunctionCallArgValue.value(arg),
           {:ok, value} <- decode(value, get_in(args_def, [name, :type]), dag) do
        {name, value}
      end
    end)
  end

  def decode(value, type, dag) do
    with {:ok, value} <- Jason.decode(value) do
      cast(value, type, dag)
    end
  end

  defp cast(value, :integer, _) when is_integer(value) do
    {:ok, value}
  end

  defp cast(value, :boolean, _) when is_boolean(value) do
    {:ok, value}
  end

  defp cast(value, :string, _) when is_binary(value) do
    {:ok, value}
  end

  defp cast(values, {:list, type}, dag) when is_list(values) do
    values =
      for value <- values do
        {:ok, value} = cast(value, type, dag)
        value
      end

    {:ok, values}
  end

  defp cast(value, module, dag) when is_binary(value) and is_atom(module) do
    # NOTE: It feels like we really need a protocol for the module to 
    # load the data from id.
    ["Dagger", name] = Module.split(module)
    name = Macro.underscore(name)
    fun = String.to_existing_atom("load_#{name}_from_id")
    {:ok, apply(Dagger.Client, fun, [dag, value])}
  end

  defp cast(value, type, _) do
    {:error, "cannot cast value #{value} to type #{type}"}
  end

  def encode(result, type) do
    with {:ok, value} <- dump(result, type) do
      Jason.encode(value)
    end
  end

  defp dump(value, :integer) when is_integer(value) do
    {:ok, value}
  end

  defp dump(value, :boolean) when is_boolean(value) do
    {:ok, value}
  end

  defp dump(value, :string) when is_binary(value) do
    {:ok, value}
  end

  defp dump(values, {:list, type}) when is_list(values) do
    values =
      for value <- values do
        {:ok, value} = dump(value, type)
        value
      end

    {:ok, values}
  end

  defp dump(%module{} = struct, module) do
    value =
      if function_exported?(module, :id, 1) do
        Dagger.ID.id!(struct)
      else
        struct
      end

    {:ok, value}
  end

  defp dump(value, type) do
    {:error, "cannot dump value #{value} to type #{type}"}
  end

  defmacro __using__(opt) do
    name = opt[:name]

    unless name do
      raise "Module name is required."
    end

    quote bind_quoted: [name: name] do
      use GenServer

      import Dagger.ModuleRuntime

      @name name
      @on_definition Dagger.ModuleRuntime
      @functions []

      Module.register_attribute(__MODULE__, :name, persist: true)
      Module.register_attribute(__MODULE__, :functions, persist: true)

      def start_link(_) do
        GenServer.start_link(__MODULE__, [], name: __MODULE__)
      end

      def init([]) do
        Dagger.ModuleRuntime.Registry.register(__MODULE__)
        {:ok, []}
      end
    end
  end
end
