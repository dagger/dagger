defmodule Dagger.Codegen.ElixirGenerator.ObjectRenderer do
  @moduledoc """
  Provides functions to render small part of Elixir object code.
  """

  alias Dagger.Codegen.ElixirGenerator.Formatter
  alias Dagger.Codegen.ElixirGenerator.Renderer
  alias Dagger.Codegen.Introspection.Types.Field
  alias Dagger.Codegen.Introspection.Types.InputValue
  alias Dagger.Codegen.Introspection.Types.Type
  alias Dagger.Codegen.Introspection.Types.TypeRef

  @doc """
  Render object type into module.
  """
  def render(type) do
    Renderer.render_module(type, render_module_body(type))
  end

  def render_module_body(type) do
    module_var = Formatter.format_var_name(type.name)

    [
      """
      alias Dagger.Core.Client
      alias Dagger.Core.QueryBuilder, as: QB
      """,
      ?\n,
      render_derive_type(type),
      ?\n,
      "defstruct [:query_builder, :client]",
      ?\n,
      ?\n,
      "@type t() :: %__MODULE__{}",
      ?\n,
      for field <- type.fields do
        [
          render_function(type, field, module_var),
          ?\n
        ]
      end
    ]
  end

  def render_function(type, field, module_var) do
    fun_name = Formatter.format_function_name(field.name)
    {optional_args, required_args} = Enum.split_with(field.args, &InputValue.is_optional?/1)

    collision? = fn required_arg ->
      Formatter.format_var_name(required_arg.name) == module_var
    end

    module_var =
      if Enum.any?(required_args, collision?) do
        module_var <> "_"
      else
        module_var
      end

    [
      Renderer.render_deprecated(field),
      ?\n,
      Renderer.render_doc(field),
      ?\n,
      render_spec(type, field, required_args, optional_args),
      ?\n,
      "def #{fun_name}(",
      render_function_args(module_var, required_args, optional_args),
      ") do",
      ?\n,
      "  query_builder = ",
      ?\n,
      render_query_builder_chain(field, module_var, required_args, optional_args),
      ?\n,
      render_return_value(type, field, module_var),
      ?\n,
      "end"
    ]
  end

  def render_return_value(type, field, module_var) do
    cond do
      TypeRef.is_list_of?(field.type, "OBJECT") ->
        output_type = Formatter.format_output_type(field.type.of_type)
        load_type_name = field.type.of_type.of_type.of_type.name

        [
          "with {:ok, items} <- Client.execute(#{module_var}.client, query_builder) do",
          ?\n,
          "  {:ok, for %{\"id\" => id} <- items do",
          ?\n,
          """
                %#{output_type}{
                  query_builder:
                    QB.query()
                    |> QB.select("load#{load_type_name}FromID")
                    |> QB.put_arg("id", id),
                  client: #{module_var}.client
                }
          """,
          ?\n,
          "  end}",
          ?\n,
          "end"
        ]

      TypeRef.is_void?(field.type) ->
        [
          "case Client.execute(#{module_var}.client, query_builder) do",
          ?\n,
          "  {:ok, _} -> :ok",
          ?\n,
          "  error -> error",
          ?\n,
          "end",
          ?\n
        ]

      TypeRef.is_scalar?(field.type) ->
        type_name =
          case field.type.of_type do
            nil -> ""
            type -> type.name
          end

        id_of_type = String.trim_trailing(type_name, "ID")

        if String.ends_with?(type_name, "ID") and id_of_type == type.name and field.name != "id" do
          type = %{
            field.type
            | of_type: %{field.type.of_type | kind: "OBJECT", name: id_of_type}
          }

          output_type = Formatter.format_output_type(type)

          [
            "with {:ok, id} <- Client.execute(#{module_var}.client, query_builder) do",
            ?\n,
            """
              {:ok, %#{output_type}{
                query_builder:
                  QB.query()
                  |> QB.select("load#{id_of_type}FromID")
                  |> QB.put_arg("id", id),
                client: #{module_var}.client
              }}
            """,
            ?\n,
            "end"
          ]
        else
          "Client.execute(#{module_var}.client, query_builder)"
        end

      TypeRef.is_list_of?(field.type, "SCALAR") ->
        "Client.execute(#{module_var}.client, query_builder)"

      TypeRef.is_list_of?(field.type, "ENUM") ->
        output_type = Formatter.format_output_type(field.type)

        """
        case Client.execute(#{module_var}.client, query_builder) do
          {:ok, enums} -> {:ok, Enum.map(enums, &#{output_type}.from_string/1)}
          error -> error
        end
        """

      TypeRef.is_enum?(field.type) ->
        output_type = Formatter.format_output_type(field.type)

        """
        case Client.execute(#{module_var}.client, query_builder) do
          {:ok, enum} -> {:ok, #{output_type}.from_string(enum)}
          error -> error
        end
        """

      true ->
        output_type = Formatter.format_output_type(field.type)

        """
        %#{output_type}{
          query_builder: query_builder,
          client: #{module_var}.client
        }
        """
    end
  end

  @doc """
  Render `@derive` module attribute.
  """
  def render_derive_type(%Type{} = type) do
    [
      if has_id_field?(type) do
        "@derive Dagger.ID"
      else
        ""
      end,
      "\n",
      if has_sync_field?(type) do
        "@derive Dagger.Sync"
      else
        ""
      end
    ]
  end

  @doc """
  Render `@spec` module attribute.
  """
  def render_spec(type, %Field{name: name} = field, required_args, optional_args) do
    map_arg = fn arg ->
      if convert_id?(arg) do
        arg.type.of_type.name
        |> String.trim_trailing("ID")
        |> Formatter.format_module()
        |> Kernel.<>(".t()")
      else
        Formatter.format_type(arg.type)
      end
    end

    required_args =
      case required_args do
        [] ->
          []

        required_args ->
          args =
            required_args
            |> Enum.map_intersperse(",", map_arg)

          [~c",", args]
      end

    optional_args =
      case optional_args do
        [] ->
          []

        optional_args ->
          args =
            optional_args
            |> Enum.map_intersperse(~c",", fn arg ->
              [
                ?{,
                arg.name |> Formatter.format_var_name() |> Renderer.render_atom(),
                ~c",",
                Formatter.format_type(arg.type),
                ?}
              ]
            end)

          [~c",", ?[, args, ?]]
      end

    [
      "@spec ",
      Formatter.format_function_name(name),
      ?(,
      "t()",
      required_args,
      optional_args,
      ?),
      " :: ",
      cond do
        TypeRef.is_void?(field.type) ->
          ":ok | {:error, term()}"

        TypeRef.is_scalar?(field.type) ->
          type_name =
            case field.type.of_type do
              nil -> ""
              type -> type.name
            end

          id_of_type = String.trim_trailing(type_name, "ID")

          type =
            if String.ends_with?(type_name, "ID") and id_of_type == type.name and
                 field.name != "id" do
              %{field.type | of_type: %{field.type.of_type | name: id_of_type}}
            else
              field.type
            end

          Formatter.format_typespec_output_type(type)

        true ->
          Formatter.format_typespec_output_type(field.type)
      end
    ]
  end

  def render_function_args(module_var, required_args, optional_args) do
    [
      "%__MODULE__{} =",
      module_var,
      render_function_required_args(required_args),
      render_function_optional_args(optional_args)
    ]
  end

  def render_function_required_args([]) do
    []
  end

  def render_function_required_args(args) do
    [
      ~c",",
      Enum.map_intersperse(args, ~c",", &Formatter.format_var_name(&1.name))
    ]
  end

  def render_function_optional_args([]), do: ""

  def render_function_optional_args(_args) do
    ", optional_args \\\\ []"
  end

  def render_put_arg(arg) do
    var_name = Formatter.format_var_name(arg.name)

    if arg.name != "id" and TypeRef.id_type?(arg.type) do
      ["Dagger.ID.id!(", var_name, ")"]
    else
      var_name
    end
  end

  def render_maybe_put_arg(arg) do
    key = arg.name |> Formatter.format_var_name() |> Renderer.render_atom()

    if TypeRef.is_list_of?(arg.type, "SCALAR") and
         TypeRef.unwrap_list(arg.type) |> TypeRef.id_type?() do
      [
        "if(optional_args[",
        key,
        "], do: Enum.map(optional_args[",
        key,
        "], &Dagger.ID.id!/1), else: nil)"
      ]
    else
      ["optional_args[", key, ~c"]"]
    end
  end

  def convert_id?(%InputValue{name: "id"}), do: false
  def convert_id?(%InputValue{type: type_ref}), do: TypeRef.id_type?(type_ref)
  def convert_id?(%InputValue{}), do: false

  defp has_sync_field?(%Type{fields: fields}) do
    Enum.any?(fields, &(&1.name == "sync"))
  end

  defp has_id_field?(%Type{fields: fields}) do
    Enum.any?(fields, &(&1.name == "id"))
  end

  def render_query_builder_chain(field, module_var, required_args, optional_args) do
    [
      "#{module_var}.query_builder",
      "|> QB.select(\"#{field.name}\")",
      for arg <- required_args do
        ["|> QB.put_arg(", ?", arg.name, ?", ~c",", render_put_arg(arg), ")"]
      end,
      for arg <- optional_args do
        ["|> QB.maybe_put_arg(", ?", arg.name, ?", ~c",", render_maybe_put_arg(arg), ")"]
      end,
      if TypeRef.is_list_of?(field.type, "OBJECT") do
        ["|> QB.select(\"id\")"]
      else
        []
      end
    ]
  end
end
