# This file generated by `mix dagger.gen`. Please DO NOT EDIT.
defmodule Dagger.Module do
  @moduledoc "A Dagger module."
  use Dagger.QueryBuilder
  @type t() :: %__MODULE__{}
  defstruct [:selection, :client]

  (
    @doc ""
    @spec dependencies(t()) :: {:ok, [Dagger.Module.t()]} | {:error, term()}
    def dependencies(%__MODULE__{} = module) do
      selection = select(module.selection, "dependencies")

      selection =
        select(
          selection,
          "dependencies dependencyConfig description generatedSourceDirectory id initialize interfaces name objects sdk serve source withDependencies withDescription withInterface withName withObject withSDK withSource"
        )

      with {:ok, data} <- execute(selection, module.client) do
        {:ok,
         data
         |> Enum.map(fn value ->
           elem_selection = Dagger.QueryBuilder.Selection.query()
           elem_selection = select(elem_selection, "loadModuleFromID")
           elem_selection = arg(elem_selection, "id", value["id"])
           %Dagger.Module{selection: elem_selection, client: module.client}
         end)}
      end
    end
  )

  (
    @doc ""
    @spec dependency_config(t()) :: {:ok, [Dagger.ModuleDependency.t()]} | {:error, term()}
    def dependency_config(%__MODULE__{} = module) do
      selection = select(module.selection, "dependencyConfig")
      selection = select(selection, "id source")

      with {:ok, data} <- execute(selection, module.client) do
        {:ok,
         data
         |> Enum.map(fn value ->
           elem_selection = Dagger.QueryBuilder.Selection.query()
           elem_selection = select(elem_selection, "loadModuleDependencyFromID")
           elem_selection = arg(elem_selection, "id", value["id"])
           %Dagger.ModuleDependency{selection: elem_selection, client: module.client}
         end)}
      end
    end
  )

  (
    @doc ""
    @spec description(t()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def description(%__MODULE__{} = module) do
      selection = select(module.selection, "description")
      execute(selection, module.client)
    end
  )

  (
    @doc "The module's root directory containing the config file for it and its source\n\n(possibly as a subdir). It includes any generated code or updated config files\n\ncreated after initial load."
    @spec generated_source_directory(t()) :: Dagger.Directory.t()
    def generated_source_directory(%__MODULE__{} = module) do
      selection = select(module.selection, "generatedSourceDirectory")
      %Dagger.Directory{selection: selection, client: module.client}
    end
  )

  (
    @doc "A unique identifier for this Module."
    @spec id(t()) :: {:ok, Dagger.ModuleID.t()} | {:error, term()}
    def id(%__MODULE__{} = module) do
      selection = select(module.selection, "id")
      execute(selection, module.client)
    end
  )

  (
    @doc "Retrieves the module with the objects loaded via its SDK."
    @spec initialize(t()) :: Dagger.Module.t()
    def initialize(%__MODULE__{} = module) do
      selection = select(module.selection, "initialize")
      %Dagger.Module{selection: selection, client: module.client}
    end
  )

  (
    @doc ""
    @spec interfaces(t()) :: {:ok, [Dagger.TypeDef.t()]} | {:error, term()}
    def interfaces(%__MODULE__{} = module) do
      selection = select(module.selection, "interfaces")

      selection =
        select(
          selection,
          "asInput asInterface asList asObject id kind optional withConstructor withField withFunction withInterface withKind withListOf withObject withOptional"
        )

      with {:ok, data} <- execute(selection, module.client) do
        {:ok,
         data
         |> Enum.map(fn value ->
           elem_selection = Dagger.QueryBuilder.Selection.query()
           elem_selection = select(elem_selection, "loadTypeDefFromID")
           elem_selection = arg(elem_selection, "id", value["id"])
           %Dagger.TypeDef{selection: elem_selection, client: module.client}
         end)}
      end
    end
  )

  (
    @doc ""
    @spec name(t()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def name(%__MODULE__{} = module) do
      selection = select(module.selection, "name")
      execute(selection, module.client)
    end
  )

  (
    @doc ""
    @spec objects(t()) :: {:ok, [Dagger.TypeDef.t()]} | {:error, term()}
    def objects(%__MODULE__{} = module) do
      selection = select(module.selection, "objects")

      selection =
        select(
          selection,
          "asInput asInterface asList asObject id kind optional withConstructor withField withFunction withInterface withKind withListOf withObject withOptional"
        )

      with {:ok, data} <- execute(selection, module.client) do
        {:ok,
         data
         |> Enum.map(fn value ->
           elem_selection = Dagger.QueryBuilder.Selection.query()
           elem_selection = select(elem_selection, "loadTypeDefFromID")
           elem_selection = arg(elem_selection, "id", value["id"])
           %Dagger.TypeDef{selection: elem_selection, client: module.client}
         end)}
      end
    end
  )

  (
    @doc ""
    @spec sdk(t()) :: {:ok, Dagger.String.t()} | {:error, term()}
    def sdk(%__MODULE__{} = module) do
      selection = select(module.selection, "sdk")
      execute(selection, module.client)
    end
  )

  (
    @doc "Serve a module's API in the current session.\n\nNote: this can only be called once per session. In the future, it could return a stream or service to remove the side effect."
    @spec serve(t()) :: {:ok, Dagger.Void.t() | nil} | {:error, term()}
    def serve(%__MODULE__{} = module) do
      selection = select(module.selection, "serve")
      execute(selection, module.client)
    end
  )

  (
    @doc ""
    @spec source(t()) :: Dagger.ModuleSource.t()
    def source(%__MODULE__{} = module) do
      selection = select(module.selection, "source")
      %Dagger.ModuleSource{selection: selection, client: module.client}
    end
  )

  (
    @doc "Update the module configuration to use the given dependencies.\n\n## Required Arguments\n\n* `dependencies` - The module sources of dependencies to use."
    @spec with_dependencies(t(), [Dagger.ModuleSourceID.t()]) :: Dagger.Module.t()
    def with_dependencies(%__MODULE__{} = module, dependencies) do
      selection = select(module.selection, "withDependencies")
      selection = arg(selection, "dependencies", dependencies)
      %Dagger.Module{selection: selection, client: module.client}
    end
  )

  (
    @doc "Retrieves the module with the given description\n\n## Required Arguments\n\n* `description` - The description to set"
    @spec with_description(t(), Dagger.String.t()) :: Dagger.Module.t()
    def with_description(%__MODULE__{} = module, description) do
      selection = select(module.selection, "withDescription")
      selection = arg(selection, "description", description)
      %Dagger.Module{selection: selection, client: module.client}
    end
  )

  (
    @doc "This module plus the given Interface type and associated functions\n\n## Required Arguments\n\n* `iface` -"
    @spec with_interface(t(), Dagger.TypeDef.t()) :: Dagger.Module.t()
    def with_interface(%__MODULE__{} = module, iface) do
      selection = select(module.selection, "withInterface")

      (
        {:ok, id} = Dagger.TypeDef.id(iface)
        selection = arg(selection, "iface", id)
      )

      %Dagger.Module{selection: selection, client: module.client}
    end
  )

  (
    @doc "Update the module configuration to use the given name.\n\n## Required Arguments\n\n* `name` - The name to use."
    @spec with_name(t(), Dagger.String.t()) :: Dagger.Module.t()
    def with_name(%__MODULE__{} = module, name) do
      selection = select(module.selection, "withName")
      selection = arg(selection, "name", name)
      %Dagger.Module{selection: selection, client: module.client}
    end
  )

  (
    @doc "This module plus the given Object type and associated functions.\n\n## Required Arguments\n\n* `object` -"
    @spec with_object(t(), Dagger.TypeDef.t()) :: Dagger.Module.t()
    def with_object(%__MODULE__{} = module, object) do
      selection = select(module.selection, "withObject")

      (
        {:ok, id} = Dagger.TypeDef.id(object)
        selection = arg(selection, "object", id)
      )

      %Dagger.Module{selection: selection, client: module.client}
    end
  )

  (
    @doc "Update the module configuration to use the given sdk.\n\n## Required Arguments\n\n* `sdk` - The sdk to use."
    @spec with_sdk(t(), Dagger.String.t()) :: Dagger.Module.t()
    def with_sdk(%__MODULE__{} = module, sdk) do
      selection = select(module.selection, "withSDK")
      selection = arg(selection, "sdk", sdk)
      %Dagger.Module{selection: selection, client: module.client}
    end
  )

  (
    @doc "Retrieves the module with basic configuration loaded if present.\n\n## Required Arguments\n\n* `source` - The module source to initialize from."
    @spec with_source(t(), Dagger.ModuleSource.t()) :: Dagger.Module.t()
    def with_source(%__MODULE__{} = module, source) do
      selection = select(module.selection, "withSource")
      selection = arg(selection, "source", source)
      %Dagger.Module{selection: selection, client: module.client}
    end
  )
end
