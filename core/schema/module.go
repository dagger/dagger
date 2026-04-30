package schema

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/sdk"
	"github.com/dagger/dagger/dagql"
	dagqlintrospection "github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/vektah/gqlparser/v2/ast"
)

// introspectionDefaultToJSON converts a GraphQL default-value literal (as
// returned by GraphQL introspection) into a JSON-encoded value suitable for
// FunctionArg.DefaultValue. Most GraphQL literals (strings, numbers, booleans)
// are already valid JSON, but enum literals are bare identifiers (e.g. RED)
// and lists/objects can contain enums, so a typed dagql.Input — when
// available — is the only reliable way to re-encode them.
func introspectionDefaultToJSON(literal *string, argSpec dagql.InputSpec) (core.JSON, error) {
	if literal == nil {
		return nil, nil
	}
	if argSpec.Default != nil {
		encoded, err := json.Marshal(argSpec.Default)
		if err != nil {
			return nil, fmt.Errorf("marshal default %q: %w", *literal, err)
		}
		return core.JSON(encoded), nil
	}
	return core.JSON(*literal), nil
}

type moduleSchema struct{}

var _ SchemaResolvers = &moduleSchema{}

var moduleDirectives = []dagql.DirectiveSpec{
	{
		Name:        "sourceMap",
		Description: dagql.FormatDescription(`Indicates the source information for where a given field is defined.`),
		Args: dagql.NewInputSpecs(
			dagql.InputSpec{
				Name: "module",
				Type: dagql.String(""),
			},
			dagql.InputSpec{
				Name: "filename",
				Type: dagql.String(""),
			},
			dagql.InputSpec{
				Name: "line",
				Type: dagql.Int(0),
			},
			dagql.InputSpec{
				Name: "column",
				Type: dagql.Int(0),
			},
			dagql.InputSpec{
				Name: "url",
				Type: dagql.String(""),
			},
		),
		Locations: []dagql.DirectiveLocation{
			dagql.DirectiveLocationScalar,
			dagql.DirectiveLocationObject,
			dagql.DirectiveLocationFieldDefinition,
			dagql.DirectiveLocationArgumentDefinition,
			dagql.DirectiveLocationUnion,
			dagql.DirectiveLocationEnum,
			dagql.DirectiveLocationEnumValue,
			dagql.DirectiveLocationInputObject,
		},
	},
	{
		Name:        "enumValue",
		Description: dagql.FormatDescription(`Indicates the underlying value of an enum member.`),
		Args: dagql.NewInputSpecs(
			dagql.InputSpec{
				Name: "value",
				Type: dagql.String(""),
			},
		),
		Locations: []dagql.DirectiveLocation{
			dagql.DirectiveLocationEnumValue,
		},
	},
	{
		Name:        "defaultPath",
		Description: dagql.FormatDescription(`Indicates that the argument defaults to a contextual path.`),
		Args: dagql.NewInputSpecs(
			dagql.InputSpec{
				Name: "path",
				Type: dagql.String(""),
			},
		),
		Locations: []dagql.DirectiveLocation{
			dagql.DirectiveLocationArgumentDefinition,
		},
	},
	{
		Name:        "defaultAddress",
		Description: dagql.FormatDescription(`Indicates that the argument defaults to a container address.`),
		Args: dagql.NewInputSpecs(
			dagql.InputSpec{
				Name: "address",
				Type: dagql.String(""),
			},
		),
		Locations: []dagql.DirectiveLocation{
			dagql.DirectiveLocationArgumentDefinition,
		},
	},
	{
		Name:        "ignorePatterns",
		Description: dagql.FormatDescription(`Filter directory contents using .gitignore-style glob patterns.`),
		Args: dagql.NewInputSpecs(
			dagql.InputSpec{
				Name: "patterns",
				Type: dagql.ArrayInput[dagql.String](nil),
			},
		),
		Locations: []dagql.DirectiveLocation{
			dagql.DirectiveLocationArgumentDefinition,
		},
	},
	{
		Name:        "check",
		Description: dagql.FormatDescription(`Indicates that this function is a check.`),
		Args:        dagql.NewInputSpecs(),
		Locations: []dagql.DirectiveLocation{
			dagql.DirectiveLocationFieldDefinition,
		},
	},
	{
		Name:        "generate",
		Description: dagql.FormatDescription(`Indicates that this function is a generate function.`),
		Args:        dagql.NewInputSpecs(),
		Locations: []dagql.DirectiveLocation{
			dagql.DirectiveLocationFieldDefinition,
		},
	},
	{
		Name:        "up",
		Description: dagql.FormatDescription(`Indicates that this function returns a service for dagger up.`),
		Args:        dagql.NewInputSpecs(), // none
		Locations: []dagql.DirectiveLocation{
			dagql.DirectiveLocationFieldDefinition,
		},
	},
	{
		Name:        "cache",
		Description: dagql.FormatDescription(`Controls the caching behavior of a function.`),
		Args: dagql.NewInputSpecs(
			dagql.InputSpec{
				Name:        "policy",
				Description: dagql.FormatDescription(`The cache policy to use.`),
				Type:        dagql.Optional[core.FunctionCachePolicy]{},
			},
			dagql.InputSpec{
				Name:        "ttl",
				Description: dagql.FormatDescription(`The time-to-live for cached results, as a duration string (e.g. "5m", "1h30s"). Only valid with the Default policy.`),
				Type:        dagql.Optional[dagql.String]{},
			},
		),
		Locations: []dagql.DirectiveLocation{
			dagql.DirectiveLocationFieldDefinition,
		},
	},
}

func (s *moduleSchema) Install(dag *dagql.Server) {
	for _, directive := range moduleDirectives {
		dag.InstallDirective(directive)
	}

	dagql.Fields[*core.Query]{
		dagql.Func("module", s.module).
			Doc(`Create a new module.`),

		dagql.Func("typeDef", s.typeDef).
			Doc(`Create a new TypeDef.`),

		dagql.Func("__loadInputTypeDef", s.loadInputTypeDef).
			Doc(`Load an input TypeDef by name for internal server use.`).
			Args(
				dagql.Arg("name").Doc(`The name of the input type.`),
			),

		dagql.Func("__function", s.internalFunction),
		dagql.Func("__functionArg", s.functionArg),
		dagql.Func("__functionArgExact", s.internalFunctionArg),
		dagql.Func("__fieldTypeDef", s.fieldTypeDef),
		dagql.Func("__fieldTypeDefExact", s.internalFieldTypeDef),
		dagql.Func("__enumMemberTypeDef", s.enumMemberTypeDef),
		dagql.Func("__enumValueTypeDef", s.enumValueTypeDef),
		dagql.Func("__listTypeDef", s.listTypeDef),
		dagql.Func("__objectTypeDef", s.objectTypeDef),
		dagql.Func("__interfaceTypeDef", s.interfaceTypeDef),
		dagql.Func("__inputTypeDef", s.inputTypeDef),
		dagql.Func("__scalarTypeDef", s.scalarTypeDef),
		dagql.Func("__enumTypeDef", s.enumTypeDef),

		dagql.Func("generatedCode", s.generatedCode).
			Doc(`Create a code generation result, given a directory containing the generated code.`),

		dagql.Func("function", s.function).
			Doc(`Creates a function.`).
			Args(
				dagql.Arg("name").Doc(`Name of the function, in its original format from the implementation language.`),
				dagql.Arg("returnType").Doc(`Return type of the function.`),
			),

		dagql.Func("sourceMap", s.sourceMap).
			Doc(`Creates source map metadata.`).
			Args(
				dagql.Arg("module").Doc("The source module owning this source map.").Internal(),
				dagql.Arg("filename").Doc("The filename from the module source."),
				dagql.Arg("line").Doc("The line number within the filename."),
				dagql.Arg("column").Doc("The column number within the line."),
				dagql.Arg("url").Doc("The browser URL for this source map, if any.").Internal(),
			),

		dagql.FuncWithDynamicInputs("currentModule", s.currentModule, s.currentModuleCacheKey).
			Doc(`The module currently being served in the session, if any.`),

		dagql.Func("currentTypeDefs", s.currentTypeDefs).
			WithInput(dagql.CurrentSchemaInput).
			Args(
				dagql.Arg("returnAllTypes").Doc(`Return the full referenced typedef closure instead of only top-level served typedefs.`),
				dagql.Arg("hideCore").Doc(
					`Strip core API functions from the Query type, leaving only module-sourced functions (constructors, entrypoint proxies, etc.).`,
					`Core types (Container, Directory, etc.) are kept so return types and method chaining still work.`,
				),
			).
			Doc(`The TypeDef representations of the objects currently being served in the session.`),

		dagql.Func("currentFunctionCall", s.currentFunctionCall).
			WithInput(dagql.PerClientInput).
			Doc(`The FunctionCall context that the SDK caller is currently executing in.`,
				`If the caller is not currently executing in a function, this will
				return an error.`),
	}.Install(dag)

	dagql.Fields[*core.FunctionCall]{
		dagql.Func("returnValue", s.functionCallReturnValue).
			WithInput(dagql.PerClientInput).
			Doc(`Set the return value of the function call to the provided value.`).
			Args(
				dagql.Arg("value").Doc(`JSON serialization of the return value.`),
			),
		dagql.Func("returnError", s.functionCallReturnError).
			WithInput(dagql.PerClientInput).
			Doc(`Return an error from the function.`).
			Args(
				dagql.Arg("error").Doc(`The error to return.`),
			),
	}.Install(dag)

	dagql.Fields[*core.Module]{
		// sync is used by external dependencies like daggerverse
		Syncer[*core.Module]().
			Doc(`Forces evaluation of the module, including any loading into the engine and associated validation.`),

		dagql.NodeFunc("checks", s.moduleChecks).
			Experimental("This API is highly experimental and may be removed or replaced entirely.").
			Doc(`Return all checks defined by the module`).
			Args(
				dagql.Arg("include").Doc("Only include checks matching the specified patterns"),
				dagql.Arg("noGenerate").Doc("When true, only return annotated check functions; exclude generate-as-checks"),
			),

		dagql.NodeFunc("check", s.moduleCheck).
			Experimental("This API is highly experimental and may be removed or replaced entirely.").
			Doc(`Return the check defined by the module with the given name. Must match to exactly one check.`).
			Args(
				dagql.Arg("name").Doc("The name of the check to retrieve"),
			),

		dagql.NodeFunc("generators", s.moduleGenerators).
			Experimental("This API is highly experimental and may be removed or replaced entirely.").
			Doc(`Return all generators defined by the module`).
			Args(
				dagql.Arg("include").Doc("Only include generators matching the specified patterns"),
			),

		dagql.NodeFunc("services", s.moduleServices).
			Experimental("This API is highly experimental and may be removed or replaced entirely.").
			Doc(`Return all services defined by the module`).
			Args(
				dagql.Arg("include").Doc("Only include services matching the specified patterns"),
			),

		dagql.NodeFunc("generator", s.moduleGenerator).
			Experimental("This API is highly experimental and may be removed or replaced entirely.").
			Doc(`Return the generator defined by the module with the given name. Must match to exactly one generator.`).
			Args(
				dagql.Arg("name").Doc("The name of the generator to retrieve"),
			),

		dagql.Func("dependencies", s.moduleDependencies).
			Doc(`The dependencies of the module.`),

		dagql.Func("introspectionSchemaJSON", s.moduleIntrospectionSchemaJSON).
			Doc(`The introspection schema JSON file for this module.`,
				`This file represents the schema visible to the module's source code, including all core types and those from the dependencies.`,
				`Note: this is in the context of a module, so some core types may be hidden.`),

		dagql.NodeFunc("generatedContextDirectory", s.moduleGeneratedContextDirectory).
			Doc(`The generated files and directories made on top of the module source's context directory.`),

		dagql.Func("userDefaults", s.moduleUserDefaults).
			Doc(`User-defined default values, loaded from local .env files.`),

		dagql.Func("withDescription", s.moduleWithDescription).
			Doc(`Retrieves the module with the given description`).
			Args(
				dagql.Arg("description").Doc(`The description to set`),
			),

		dagql.Func("withObject", s.moduleWithObject).
			Doc(`This module plus the given Object type and associated functions.`),

		dagql.Func("withInterface", s.moduleWithInterface).
			Doc(`This module plus the given Interface type and associated functions`),

		dagql.Func("withEnum", s.moduleWithEnum).
			Doc(`This module plus the given Enum type and associated values`),

		dagql.Func("runtime", s.moduleRuntime).
			IsPersistable().
			Doc(`The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.`),

		dagql.NodeFunc("serve", s.moduleServe).
			DoNotCache(`Mutates the calling session's global schema.`).
			Doc(`Serve a module's API in the current session.`,
				`Note: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.`).
			Args(
				dagql.Arg("includeDependencies").Doc("Expose the dependencies of this module to the client"),
				dagql.Arg("entrypoint").Doc("Install the module as the entrypoint, promoting its main-object methods onto the Query root"),
			),

		dagql.NodeFunc("_implementationScoped", s.moduleImplementationScoped).
			Doc(`The module object scoped to implementation identity only, i.e. source code and dependency content rather than client-specific provenance.`),
	}.Install(dag)

	dagql.Fields[*core.CurrentModule]{
		dagql.Func("dependencies", s.currentModuleDependencies).
			Doc(`The dependencies of the module.`),

		dagql.NodeFunc("generatedContextDirectory", s.currentModuleGeneratedContextDirectory).
			Doc("The generated files and directories made on top of the module source's context directory."),

		dagql.Func("name", s.currentModuleName).
			Doc(`The name of the module being executed in`),

		dagql.NodeFunc("source", s.currentModuleSource).
			Doc(`The directory containing the module's source code loaded into the engine (plus any generated code that may have been created).`),

		dagql.NodeFunc("workdir", s.currentModuleWorkdir).
			WithInput(dagql.PerClientInput).
			Doc(`Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.`).
			Args(
				dagql.Arg("path").Doc(`Location of the directory to access (e.g., ".").`),
				dagql.Arg("exclude").Doc(`Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).`),
				dagql.Arg("include").Doc(`Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).`),
				dagql.Arg("gitignore").Doc(`Apply .gitignore filter rules inside the directory`),
			),

		dagql.NodeFunc("workdirFile", s.currentModuleWorkdirFile).
			WithInput(dagql.PerClientInput).
			Doc(`Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.`).
			Args(
				dagql.Arg("path").Doc(`Location of the file to retrieve (e.g., "README.md").`),
			),

		dagql.Func("generators", s.currentModuleGenerators).
			Experimental("This API is highly experimental and may be removed or replaced entirely.").
			Doc(`Return all generators defined by the module`).
			Args(
				dagql.Arg("include").Doc("Only include generators matching the specified patterns"),
			),
	}.Install(dag)

	dagql.Fields[*core.Function]{
		dagql.Func("withDescription", s.functionWithDescription).
			Doc(`Returns the function with the given doc string.`).
			Args(
				dagql.Arg("description").Doc(`The doc string to set.`),
			),

		dagql.Func("withDeprecated", s.functionWithDeprecated).
			Doc(`Returns the function with the provided deprecation reason.`).
			Args(
				dagql.Arg("reason").Doc(`Reason or migration path describing the deprecation.`),
			),

		dagql.Func("withCheck", s.functionWithCheck).
			Doc(`Returns the function with a flag indicating it's a check.`),

		dagql.Func("withGenerator", s.functionWithGenerator).
			Doc(`Returns the function with a flag indicating it's a generator.`),

		dagql.Func("withUp", s.functionWithUp).
			Doc(`Returns the function with a flag indicating it returns a service for dagger up.`),

		dagql.Func("withSourceMap", s.functionWithSourceMap).
			Doc(`Returns the function with the given source map.`).
			Args(
				dagql.Arg("sourceMap").Doc(`The source map for the function definition.`),
			),

		dagql.Func("withArg", s.functionWithArg).
			Doc(`Returns the function with the provided argument`).
			Args(
				dagql.Arg("name").Doc(`The name of the argument`),
				dagql.Arg("typeDef").Doc(`The type of the argument`),
				dagql.Arg("description").Doc(`A doc string for the argument, if any`),
				dagql.Arg("defaultValue").Doc(`A default value to use for this argument if not explicitly set by the caller, if any`),
				dagql.Arg("defaultPath").Doc(`If the argument is a Directory or File type, default to load path from context directory, relative to root directory.`),
				dagql.Arg("ignore").Doc(`Patterns to ignore when loading the contextual argument value.`),
				dagql.Arg("sourceMap").Doc(`The source map for the argument definition.`),
				dagql.Arg("deprecated").Doc(`If deprecated, the reason or migration path.`),
			),

		dagql.Func("withCachePolicy", s.functionWithCachePolicy).
			Doc(`Returns the function updated to use the provided cache policy.`).
			Args(
				dagql.Arg("policy").Doc(`The cache policy to use.`),
				dagql.Arg("timeToLive").Doc(`The TTL for the cache policy, if applicable. Provided as a duration string, e.g. "5m", "1h30s".`),
			),
		dagql.Func("__withReturnType", s.functionWithReturnType),
		dagql.Func("__withSourceMap", s.functionWithSourceMapResult),
		dagql.Func("__withArg", s.functionWithArgResult),
		dagql.Func("__asConstructor", s.functionAsConstructor),
	}.Install(dag)

	dagql.Fields[*core.Function]{
		dagql.Func("args", s.functionArgs).
			Doc(`Arguments accepted by the function, if any.`),
		dagql.Func("returnType", s.functionReturnType).
			Doc(`The type returned by the function.`),
	}.Install(dag)

	dagql.Fields[*core.FunctionArg]{
		dagql.Func("__withTypeDef", s.functionArgWithTypeDef),
		dagql.Func("__withSourceMap", s.functionArgWithSourceMap),
		dagql.Func("__withDefaultValue", s.functionArgWithDefaultValue),
		dagql.Func("__withDefaultPath", s.functionArgWithDefaultPath),
		dagql.Func("__withDefaultAddress", s.functionArgWithDefaultAddress),
		dagql.Func("__withIgnore", s.functionArgWithIgnore),
	}.Install(dag)

	dagql.Fields[*core.FunctionArg]{
		dagql.Func("typeDef", s.functionArgTypeDef).
			Doc(`The type of the argument.`),
	}.Install(dag)

	dagql.Fields[*core.FunctionCallArgValue]{}.Install(dag)

	dagql.Fields[*core.SourceMap]{}.Install(dag)

	dagql.Fields[*core.TypeDef]{
		dagql.Func("withOptional", s.typeDefWithOptional).
			Doc(`Sets whether this type can be set to null.`),

		dagql.Func("withKind", s.typeDefWithKind).
			Doc(`Sets the kind of the type.`),

		dagql.Func("withScalar", s.typeDefWithScalar).
			Doc(`Returns a TypeDef of kind Scalar with the provided name.`).
			Args(
				dagql.Arg("sourceModuleName").Doc(`The module owning this scalar type.`).Internal(),
			),

		dagql.Func("withListOf", s.typeDefWithListOf).
			Doc(`Returns a TypeDef of kind List with the provided type for its elements.`),

		dagql.Func("withObject", s.typeDefWithObject).
			Doc(`Returns a TypeDef of kind Object with the provided name.`,
				`Note that an object's fields and functions may be omitted if the
				intent is only to refer to an object. This is how functions are able to
				return their own object, or any other circular reference.`).
			Args(
				dagql.Arg("sourceModuleName").Doc(`The module owning this object type.`).Internal(),
			),

		dagql.Func("withInterface", s.typeDefWithInterface).
			Doc(`Returns a TypeDef of kind Interface with the provided name.`).
			Args(
				dagql.Arg("sourceModuleName").Doc(`The module owning this interface type.`).Internal(),
			),

		dagql.Func("withField", s.typeDefWithObjectField).
			Doc(`Adds a static field for an Object TypeDef, failing if the type is not an object.`).
			Args(
				dagql.Arg("name").Doc(`The name of the field in the object`),
				dagql.Arg("typeDef").Doc(`The type of the field`),
				dagql.Arg("description").Doc(`A doc string for the field, if any`),
				dagql.Arg("sourceMap").Doc(`The source map for the field definition.`),
				dagql.Arg("deprecated").Doc(`If deprecated, the reason or migration path.`),
			),

		dagql.Func("withFunction", s.typeDefWithFunction).
			Doc(`Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.`),

		dagql.Func("withConstructor", s.typeDefWithObjectConstructor).
			Doc(`Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.`),

		dagql.Func("withEnum", s.typeDefWithEnum).
			Doc(`Returns a TypeDef of kind Enum with the provided name.`,
				`Note that an enum's values may be omitted if the intent is only to refer to an enum.
				This is how functions are able to return their own, or any other circular reference.`).
			Args(
				dagql.Arg("name").Doc(`The name of the enum`),
				dagql.Arg("description").Doc(`A doc string for the enum, if any`),
				dagql.Arg("sourceMap").Doc(`The source map for the enum definition.`),
				dagql.Arg("sourceModuleName").Doc(`The module owning this enum type.`).Internal(),
			),

		dagql.Func("withEnumValue", s.typeDefWithEnumValue).
			Doc(`Adds a static value for an Enum TypeDef, failing if the type is not an enum.`).
			Deprecated("Use `withEnumMember` instead").
			Args(
				dagql.Arg("value").Doc(`The name of the value in the enum`),
				dagql.Arg("description").Doc(`A doc string for the value, if any`),
				dagql.Arg("sourceMap").Doc(`The source map for the enum value definition.`),
				dagql.Arg("deprecated").Doc(`If deprecated, the reason or migration path.`),
			),

		dagql.Func("withEnumMember", s.typeDefWithEnumMember).
			View(AllVersion).
			Doc(`Adds a static value for an Enum TypeDef, failing if the type is not an enum.`).
			Args(
				dagql.Arg("name").Doc(`The name of the member in the enum`),
				dagql.Arg("value").Doc(`The value of the member in the enum`),
				dagql.Arg("description").Doc(`A doc string for the member, if any`),
				dagql.Arg("sourceMap").Doc(`The source map for the enum member definition.`),
				dagql.Arg("deprecated").Doc(`If deprecated, the reason or migration path.`),
			),
		dagql.Func("__withListTypeDef", s.typeDefWithListTypeDef),
		dagql.Func("__withObjectTypeDef", s.typeDefWithObjectTypeDef),
		dagql.Func("__withInterfaceTypeDef", s.typeDefWithInterfaceTypeDef),
		dagql.Func("__withInputTypeDef", s.typeDefWithInputTypeDef),
		dagql.Func("__withScalarTypeDef", s.typeDefWithScalarTypeDef),
		dagql.Func("__withEnumTypeDef", s.typeDefWithEnumTypeDef),
	}.Install(dag)
	dagql.Fields[*core.TypeDef]{
		dagql.Func("asList", s.typeDefAsList).
			Doc(`If kind is LIST, the list-specific type definition. If kind is not LIST, this will be null.`),
		dagql.Func("asObject", s.typeDefAsObject).
			Doc(`If kind is OBJECT, the object-specific type definition. If kind is not OBJECT, this will be null.`),
		dagql.Func("asInterface", s.typeDefAsInterface).
			Doc(`If kind is INTERFACE, the interface-specific type definition. If kind is not INTERFACE, this will be null.`),
		dagql.Func("asInput", s.typeDefAsInput).
			Doc(`If kind is INPUT, the input-specific type definition. If kind is not INPUT, this will be null.`),
		dagql.Func("asScalar", s.typeDefAsScalar).
			Doc(`If kind is SCALAR, the scalar-specific type definition. If kind is not SCALAR, this will be null.`),
		dagql.Func("asEnum", s.typeDefAsEnum).
			Doc(`If kind is ENUM, the enum-specific type definition. If kind is not ENUM, this will be null.`),
	}.Install(dag)

	dagql.Fields[*core.ObjectTypeDef]{
		dagql.Func("fields", s.objectTypeDefFields).
			Doc(`Static fields defined on this object, if any.`),
		dagql.Func("functions", s.objectTypeDefFunctions).
			Doc(`Functions defined on this object, if any.`),
		dagql.Func("constructor", s.objectTypeDefConstructor).
			Doc(`The function used to construct new instances of this object, if any.`),
		dagql.Func("__withName", s.objectTypeDefWithName),
		dagql.Func("__withSourceMap", s.objectTypeDefWithSourceMap),
		dagql.Func("__withSourceModuleName", s.objectTypeDefWithSourceModuleName),
		dagql.Func("__withField", s.objectTypeDefWithField),
		dagql.Func("__withFunction", s.objectTypeDefWithFunction),
		dagql.Func("__withConstructor", s.objectTypeDefWithConstructor),
	}.Install(dag)
	dagql.Fields[*core.InterfaceTypeDef]{
		dagql.Func("functions", s.interfaceTypeDefFunctions).
			Doc(`Functions defined on this interface, if any.`),
		dagql.Func("__withName", s.interfaceTypeDefWithName),
		dagql.Func("__withSourceMap", s.interfaceTypeDefWithSourceMap),
		dagql.Func("__withSourceModuleName", s.interfaceTypeDefWithSourceModuleName),
		dagql.Func("__withFunction", s.interfaceTypeDefWithFunction),
	}.Install(dag)
	dagql.Fields[*core.InputTypeDef]{
		dagql.Func("fields", s.inputTypeDefFields).
			Doc(`Static fields defined on this input object, if any.`),
		dagql.Func("__withField", s.inputTypeDefWithField),
	}.Install(dag)
	dagql.Fields[*core.FieldTypeDef]{
		dagql.Func("typeDef", s.fieldTypeDefTypeDef).
			Doc(`The type of the field.`),
		dagql.Func("__withTypeDef", s.fieldTypeDefWithTypeDef),
		dagql.Func("__withSourceMap", s.fieldTypeDefWithSourceMap),
	}.Install(dag)
	dagql.Fields[*core.ListTypeDef]{
		dagql.Func("elementTypeDef", s.listElementTypeDef).
			Doc(`The type of the elements in the list.`),
		dagql.Func("__withElementTypeDef", s.listTypeDefWithElementTypeDef),
	}.Install(dag)
	dagql.Fields[*core.ScalarTypeDef]{}.Install(dag)
	dagql.Fields[*core.EnumTypeDef]{
		dagql.Func("values", s.enumTypeDefValues).
			Deprecated("use members instead").
			Doc(`The members of the enum.`),
		dagql.Func("members", s.enumTypeDefMembers).
			Doc(`The members of the enum.`),
		dagql.Func("__withName", s.enumTypeDefWithName),
		dagql.Func("__withSourceMap", s.enumTypeDefWithSourceMap),
		dagql.Func("__withSourceModuleName", s.enumTypeDefWithSourceModuleName),
		dagql.Func("__withMember", s.enumTypeDefWithMember),
	}.Install(dag)
	dagql.Fields[*core.EnumMemberTypeDef]{
		dagql.Func("__withName", s.enumMemberTypeDefWithName),
		dagql.Func("__withSourceMap", s.enumMemberTypeDefWithSourceMap),
	}.Install(dag)
}

func (s *moduleSchema) loadInputTypeDef(ctx context.Context, self *core.Query, args struct {
	Name string
}) (*core.TypeDef, error) {
	deps, err := self.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current module: %w", err)
	}
	dag, err := deps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current schema: %w", err)
	}
	typeDefs, err := deps.TypeDefs(ctx, dag)
	if err != nil {
		return nil, err
	}
	for _, typeDef := range typeDefs {
		if typeDef.Self() == nil || typeDef.Self().Kind != core.TypeDefKindInput || !typeDef.Self().AsInput.Valid {
			continue
		}
		if typeDef.Self().AsInput.Value.Self().Name == args.Name {
			return typeDef.Self(), nil
		}
	}
	return nil, fmt.Errorf("input type %q not found", args.Name)
}

func (s *moduleSchema) typeDef(ctx context.Context, _ *core.Query, args struct{}) (*core.TypeDef, error) {
	return &core.TypeDef{}, nil
}

func (s *moduleSchema) functionArg(ctx context.Context, _ *core.Query, args struct {
	Name           string
	TypeDef        core.TypeDefID
	Description    string    `default:""`
	DefaultValue   core.JSON `default:""`
	DefaultPath    string    `default:""`
	DefaultAddress string    `default:""`
	Ignore         []string  `default:"[]"`
	SourceMap      dagql.Optional[core.SourceMapID]
	Deprecated     *string
}) (*core.FunctionArg, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	typeDef, err := args.TypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode arg type: %w", err)
	}
	if args.DefaultPath != "" || args.DefaultAddress != "" {
		typeDef, err = s.withOptional(ctx, dag, typeDef)
		if err != nil {
			return nil, fmt.Errorf("failed to optionalize arg type: %w", err)
		}
	}
	arg := core.NewFunctionArg(args.Name, typeDef, args.Description, args.DefaultValue, args.DefaultPath, args.DefaultAddress, args.Ignore, args.Deprecated)
	if arg.IsWorkspace() {
		typeDef, err = s.withOptional(ctx, dag, arg.TypeDef)
		if err != nil {
			return nil, fmt.Errorf("failed to optionalize workspace arg type: %w", err)
		}
		arg.TypeDef = typeDef
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	if sourceMap.Self() != nil {
		arg.SourceMap = dagql.NonNull(sourceMap)
	}
	return arg, nil
}

func (s *moduleSchema) internalFunctionArg(ctx context.Context, _ *core.Query, args struct {
	Name           string
	TypeDef        core.TypeDefID
	Description    string    `default:""`
	DefaultValue   core.JSON `default:""`
	DefaultPath    string    `default:""`
	DefaultAddress string    `default:""`
	Ignore         []string  `default:"[]"`
	SourceMap      dagql.Optional[core.SourceMapID]
	Deprecated     *string
}) (*core.FunctionArg, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	typeDef, err := args.TypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode arg type: %w", err)
	}
	if args.DefaultPath != "" || args.DefaultAddress != "" {
		typeDef, err = s.withOptional(ctx, dag, typeDef)
		if err != nil {
			return nil, fmt.Errorf("failed to optionalize arg type: %w", err)
		}
	}
	arg := &core.FunctionArg{
		Name:           args.Name,
		Description:    args.Description,
		TypeDef:        typeDef,
		DefaultValue:   args.DefaultValue,
		DefaultPath:    args.DefaultPath,
		DefaultAddress: args.DefaultAddress,
		Ignore:         args.Ignore,
		Deprecated:     args.Deprecated,
		OriginalName:   args.Name,
	}
	if arg.IsWorkspace() {
		typeDef, err = s.withOptional(ctx, dag, arg.TypeDef)
		if err != nil {
			return nil, fmt.Errorf("failed to optionalize workspace arg type: %w", err)
		}
		arg.TypeDef = typeDef
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	if sourceMap.Self() != nil {
		arg.SourceMap = dagql.NonNull(sourceMap)
	}
	return arg, nil
}

func (s *moduleSchema) fieldTypeDef(ctx context.Context, _ *core.Query, args struct {
	Name        string
	TypeDef     core.TypeDefID
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
	Deprecated  *string
}) (*core.FieldTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	typeDef, err := args.TypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode field type: %w", err)
	}
	field := core.NewFieldTypeDef(args.Name, typeDef, args.Description, args.Deprecated)
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	if sourceMap.Self() != nil {
		field.SourceMap = dagql.NonNull(sourceMap)
	}
	return field, nil
}

func (s *moduleSchema) internalFieldTypeDef(ctx context.Context, _ *core.Query, args struct {
	Name        string
	TypeDef     core.TypeDefID
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
	Deprecated  *string
}) (*core.FieldTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	typeDef, err := args.TypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode field type: %w", err)
	}
	field := &core.FieldTypeDef{
		Name:         args.Name,
		Description:  args.Description,
		TypeDef:      typeDef,
		Deprecated:   args.Deprecated,
		OriginalName: args.Name,
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	if sourceMap.Self() != nil {
		field.SourceMap = dagql.NonNull(sourceMap)
	}
	return field, nil
}

func (s *moduleSchema) enumMemberTypeDef(ctx context.Context, _ *core.Query, args struct {
	Name        string
	Value       string `default:""`
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
	Deprecated  *string
}) (*core.EnumMemberTypeDef, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return core.NewEnumMemberTypeDef(args.Name, args.Value, args.Description, args.Deprecated, sourceMap), nil
}

func (s *moduleSchema) enumValueTypeDef(ctx context.Context, _ *core.Query, args struct {
	Name        string
	Value       string `default:""`
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
	Deprecated  *string
}) (*core.EnumMemberTypeDef, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return core.NewEnumValueTypeDef(args.Name, args.Value, args.Description, args.Deprecated, sourceMap), nil
}

func (s *moduleSchema) listTypeDef(ctx context.Context, _ *core.Query, args struct {
	ElementTypeDef core.TypeDefID
}) (*core.ListTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	elem, err := args.ElementTypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return &core.ListTypeDef{ElementTypeDef: elem}, nil
}

func (s *moduleSchema) objectTypeDef(ctx context.Context, _ *core.Query, args struct {
	Name             string
	Description      string `default:""`
	SourceMap        dagql.Optional[core.SourceMapID]
	Deprecated       *string
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.ObjectTypeDef, error) {
	obj := core.NewObjectTypeDef(args.Name, args.Description, args.Deprecated)
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	if sourceMap.Self() != nil {
		obj.SourceMap = dagql.NonNull(sourceMap)
	}
	if args.SourceModuleName.Valid {
		obj.SourceModuleName = string(args.SourceModuleName.Value)
	}
	return obj, nil
}

func (s *moduleSchema) interfaceTypeDef(ctx context.Context, _ *core.Query, args struct {
	Name             string
	Description      string `default:""`
	SourceMap        dagql.Optional[core.SourceMapID]
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.InterfaceTypeDef, error) {
	iface := core.NewInterfaceTypeDef(args.Name, args.Description)
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	if sourceMap.Self() != nil {
		iface.SourceMap = dagql.NonNull(sourceMap)
	}
	if args.SourceModuleName.Valid {
		iface.SourceModuleName = string(args.SourceModuleName.Value)
	}
	return iface, nil
}

func (s *moduleSchema) inputTypeDef(ctx context.Context, _ *core.Query, args struct {
	Name string
}) (*core.InputTypeDef, error) {
	return &core.InputTypeDef{Name: args.Name}, nil
}

func (s *moduleSchema) withOptional(
	ctx context.Context,
	dag *dagql.Server,
	inst dagql.ObjectResult[*core.TypeDef],
) (dagql.ObjectResult[*core.TypeDef], error) {
	if err := dag.Select(ctx, inst, &inst, dagql.Selector{
		Field: "withOptional",
		Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(true)}},
	}); err != nil {
		return inst, fmt.Errorf("optionalize typedef: %w", err)
	}
	return inst, nil
}

func (s *moduleSchema) scalarTypeDef(ctx context.Context, _ *core.Query, args struct {
	Name             string
	Description      string                       `default:""`
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.ScalarTypeDef, error) {
	scalar := core.NewScalarTypeDef(args.Name, args.Description)
	if args.SourceModuleName.Valid {
		scalar.SourceModuleName = string(args.SourceModuleName.Value)
	}
	return scalar, nil
}

func (s *moduleSchema) enumTypeDef(ctx context.Context, _ *core.Query, args struct {
	Name             string
	Description      string `default:""`
	SourceMap        dagql.Optional[core.SourceMapID]
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.EnumTypeDef, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	enum := core.NewEnumTypeDef(args.Name, args.Description, sourceMap)
	if args.SourceModuleName.Valid {
		enum.SourceModuleName = string(args.SourceModuleName.Value)
	}
	return enum, nil
}

func (s *moduleSchema) typeDefWithOptional(ctx context.Context, def *core.TypeDef, args struct {
	Optional bool
}) (*core.TypeDef, error) {
	return def.WithOptional(args.Optional), nil
}

func (s *moduleSchema) typeDefWithKind(ctx context.Context, def *core.TypeDef, args struct {
	Kind core.TypeDefKind
}) (*core.TypeDef, error) {
	return def.WithKind(args.Kind), nil
}

func (s *moduleSchema) typeDefWithScalar(ctx context.Context, def *core.TypeDef, args struct {
	Name             string
	Description      string                       `default:""`
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("scalar type def must have a name")
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	var scalar dagql.ObjectResult[*core.ScalarTypeDef]
	if err := dag.Select(ctx, dag.Root(), &scalar, dagql.Selector{
		Field: "__scalarTypeDef",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(args.Name)},
			{Name: "description", Value: dagql.String(args.Description)},
			{Name: "sourceModuleName", Value: dagql.Opt(args.SourceModuleName.Value)},
		},
	}); err != nil {
		return nil, err
	}
	return def.WithScalar(scalar), nil
}

func (s *moduleSchema) typeDefWithListOf(ctx context.Context, def *core.TypeDef, args struct {
	ElementType core.TypeDefID
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	elemType, err := args.ElementType.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	elemTypeID, err := elemType.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get element type id: %w", err)
	}
	var list dagql.ObjectResult[*core.ListTypeDef]
	if err := dag.Select(ctx, dag.Root(), &list, dagql.Selector{
		Field: "__listTypeDef",
		Args: []dagql.NamedInput{
			{Name: "elementTypeDef", Value: dagql.NewID[*core.TypeDef](elemTypeID)},
		},
	}); err != nil {
		return nil, err
	}
	return def.WithListOf(list), nil
}

func (s *moduleSchema) typeDefWithObject(ctx context.Context, def *core.TypeDef, args struct {
	Name             string
	Description      string `default:""`
	SourceMap        dagql.Optional[core.SourceMapID]
	Deprecated       *string
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("object type def must have a name")
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	var obj dagql.ObjectResult[*core.ObjectTypeDef]
	if err := dag.Select(ctx, dag.Root(), &obj, dagql.Selector{
		Field: "__objectTypeDef",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(args.Name)},
			{Name: "description", Value: dagql.String(args.Description)},
			{Name: "sourceMap", Value: optID(sourceMap)},
			{Name: "deprecated", Value: optString(args.Deprecated)},
			{Name: "sourceModuleName", Value: dagql.Opt(args.SourceModuleName.Value)},
		},
	}); err != nil {
		return nil, err
	}
	return def.WithObject(obj), nil
}

//nolint:dupl // symmetric with typeDefWithEnum; sharing hides the Interface vs Enum kinds
func (s *moduleSchema) typeDefWithInterface(ctx context.Context, def *core.TypeDef, args struct {
	Name             string
	Description      string `default:""`
	SourceMap        dagql.Optional[core.SourceMapID]
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("interface type def must have a name")
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	var iface dagql.ObjectResult[*core.InterfaceTypeDef]
	if err := dag.Select(ctx, dag.Root(), &iface, dagql.Selector{
		Field: "__interfaceTypeDef",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(args.Name)},
			{Name: "description", Value: dagql.String(args.Description)},
			{Name: "sourceMap", Value: optID(sourceMap)},
			{Name: "sourceModuleName", Value: dagql.Opt(args.SourceModuleName.Value)},
		},
	}); err != nil {
		return nil, err
	}
	return def.WithInterface(iface), nil
}

func (s *moduleSchema) typeDefWithObjectField(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	TypeDef     core.TypeDefID
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
	Deprecated  *string
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	fieldType, err := args.TypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	fieldTypeID, err := fieldType.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get field type id: %w", err)
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	var field dagql.ObjectResult[*core.FieldTypeDef]
	if err := dag.Select(ctx, dag.Root(), &field, dagql.Selector{
		Field: "__fieldTypeDef",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(args.Name)},
			{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](fieldTypeID)},
			{Name: "description", Value: dagql.String(args.Description)},
			{Name: "sourceMap", Value: optID(sourceMap)},
			{Name: "deprecated", Value: optString(args.Deprecated)},
		},
	}); err != nil {
		return nil, err
	}
	var obj dagql.ObjectResult[*core.ObjectTypeDef]
	if err := dag.Select(ctx, def.AsObject.Value, &obj, dagql.Selector{
		Field: "__withField",
		Args:  []dagql.NamedInput{{Name: "field", Value: idInput(field)}},
	}); err != nil {
		return nil, err
	}
	return def.WithObject(obj), nil
}

func (s *moduleSchema) typeDefWithFunction(ctx context.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	fn, err := args.Function.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	var err2 error
	switch def.Kind {
	case core.TypeDefKindObject:
		var obj dagql.ObjectResult[*core.ObjectTypeDef]
		if err2 = dag.Select(ctx, def.AsObject.Value, &obj, dagql.Selector{
			Field: "__withFunction",
			Args:  []dagql.NamedInput{{Name: "function", Value: idInput(fn)}},
		}); err2 != nil {
			return nil, err2
		}
		return def.WithObject(obj), nil
	case core.TypeDefKindInterface:
		var iface dagql.ObjectResult[*core.InterfaceTypeDef]
		if err2 = dag.Select(ctx, def.AsInterface.Value, &iface, dagql.Selector{
			Field: "__withFunction",
			Args:  []dagql.NamedInput{{Name: "function", Value: idInput(fn)}},
		}); err2 != nil {
			return nil, err2
		}
		return def.WithInterface(iface), nil
	default:
		return nil, fmt.Errorf("cannot add function to type: %s", def.Kind)
	}
}

func (s *moduleSchema) typeDefWithObjectConstructor(ctx context.Context, def *core.TypeDef, args struct {
	Function core.FunctionID
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	inst, err := args.Function.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	var fn dagql.ObjectResult[*core.Function]
	if err := dag.Select(ctx, inst, &fn, dagql.Selector{Field: "__asConstructor"}); err != nil {
		return nil, err
	}
	var obj dagql.ObjectResult[*core.ObjectTypeDef]
	if err := dag.Select(ctx, def.AsObject.Value, &obj, dagql.Selector{
		Field: "__withConstructor",
		Args:  []dagql.NamedInput{{Name: "function", Value: idInput(fn)}},
	}); err != nil {
		return nil, err
	}
	return def.WithObject(obj), nil
}

//nolint:dupl // symmetric with typeDefWithInterface; sharing hides the Enum vs Interface kinds
func (s *moduleSchema) typeDefWithEnum(ctx context.Context, def *core.TypeDef, args struct {
	Name             string
	Description      string `default:""`
	SourceMap        dagql.Optional[core.SourceMapID]
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.TypeDef, error) {
	if args.Name == "" {
		return nil, fmt.Errorf("enum type def must have a name")
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	var enum dagql.ObjectResult[*core.EnumTypeDef]
	if err := dag.Select(ctx, dag.Root(), &enum, dagql.Selector{
		Field: "__enumTypeDef",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(args.Name)},
			{Name: "description", Value: dagql.String(args.Description)},
			{Name: "sourceMap", Value: optID(sourceMap)},
			{Name: "sourceModuleName", Value: dagql.Opt(args.SourceModuleName.Value)},
		},
	}); err != nil {
		return nil, err
	}
	return def.WithEnum(enum), nil
}

func (s *moduleSchema) typeDefWithEnumValue(ctx context.Context, def *core.TypeDef, args struct {
	Value       string
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
	Deprecated  *string
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	var member dagql.ObjectResult[*core.EnumMemberTypeDef]
	if err := dag.Select(ctx, dag.Root(), &member, dagql.Selector{
		Field: "__enumValueTypeDef",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(args.Value)},
			{Name: "value", Value: dagql.String(args.Value)},
			{Name: "description", Value: dagql.String(args.Description)},
			{Name: "sourceMap", Value: optID(sourceMap)},
			{Name: "deprecated", Value: optString(args.Deprecated)},
		},
	}); err != nil {
		return nil, err
	}
	var enum dagql.ObjectResult[*core.EnumTypeDef]
	if err := dag.Select(ctx, def.AsEnum.Value, &enum, dagql.Selector{
		Field: "__withMember",
		Args:  []dagql.NamedInput{{Name: "member", Value: idInput(member)}},
	}); err != nil {
		return nil, err
	}
	return def.WithEnum(enum), nil
}

func (s *moduleSchema) typeDefWithEnumMember(ctx context.Context, def *core.TypeDef, args struct {
	Name        string
	Value       string `default:""`
	Description string `default:""`
	SourceMap   dagql.Optional[core.SourceMapID]
	Deprecated  *string
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}

	if !supportEnumMembers(ctx) {
		legacyValue := args.Value
		if legacyValue == "" {
			legacyValue = args.Name
		}
		var member dagql.ObjectResult[*core.EnumMemberTypeDef]
		if err := dag.Select(ctx, dag.Root(), &member, dagql.Selector{
			Field: "__enumValueTypeDef",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(args.Name)},
				{Name: "value", Value: dagql.String(legacyValue)},
				{Name: "description", Value: dagql.String(args.Description)},
				{Name: "sourceMap", Value: optID(sourceMap)},
				{Name: "deprecated", Value: optString(args.Deprecated)},
			},
		}); err != nil {
			return nil, err
		}
		var enum dagql.ObjectResult[*core.EnumTypeDef]
		if err := dag.Select(ctx, def.AsEnum.Value, &enum, dagql.Selector{
			Field: "__withMember",
			Args:  []dagql.NamedInput{{Name: "member", Value: idInput(member)}},
		}); err != nil {
			return nil, err
		}
		return def.WithEnum(enum), nil
	}
	var member dagql.ObjectResult[*core.EnumMemberTypeDef]
	if err := dag.Select(ctx, dag.Root(), &member, dagql.Selector{
		Field: "__enumMemberTypeDef",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(args.Name)},
			{Name: "value", Value: dagql.String(args.Value)},
			{Name: "description", Value: dagql.String(args.Description)},
			{Name: "sourceMap", Value: optID(sourceMap)},
			{Name: "deprecated", Value: optString(args.Deprecated)},
		},
	}); err != nil {
		return nil, err
	}
	var enum dagql.ObjectResult[*core.EnumTypeDef]
	if err := dag.Select(ctx, def.AsEnum.Value, &enum, dagql.Selector{
		Field: "__withMember",
		Args:  []dagql.NamedInput{{Name: "member", Value: idInput(member)}},
	}); err != nil {
		return nil, err
	}
	return def.WithEnum(enum), nil
}

func (s *moduleSchema) typeDefWithListTypeDef(ctx context.Context, def *core.TypeDef, args struct {
	ListTypeDef core.ListTypeDefID
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	list, err := args.ListTypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode list type def: %w", err)
	}
	return def.WithListTypeDef(list), nil
}

func (s *moduleSchema) typeDefWithObjectTypeDef(ctx context.Context, def *core.TypeDef, args struct {
	ObjectTypeDef core.ObjectTypeDefID
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	obj, err := args.ObjectTypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode object type def: %w", err)
	}
	return def.WithObjectTypeDef(obj), nil
}

func (s *moduleSchema) typeDefWithInterfaceTypeDef(ctx context.Context, def *core.TypeDef, args struct {
	InterfaceTypeDef core.InterfaceTypeDefID
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	iface, err := args.InterfaceTypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode interface type def: %w", err)
	}
	return def.WithInterfaceTypeDef(iface), nil
}

func (s *moduleSchema) typeDefWithInputTypeDef(ctx context.Context, def *core.TypeDef, args struct {
	InputTypeDef core.InputTypeDefID
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	input, err := args.InputTypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode input type def: %w", err)
	}
	return def.WithInputTypeDef(input), nil
}

func (s *moduleSchema) typeDefWithScalarTypeDef(ctx context.Context, def *core.TypeDef, args struct {
	ScalarTypeDef core.ScalarTypeDefID
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	scalar, err := args.ScalarTypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode scalar type def: %w", err)
	}
	return def.WithScalarTypeDef(scalar), nil
}

func (s *moduleSchema) typeDefWithEnumTypeDef(ctx context.Context, def *core.TypeDef, args struct {
	EnumTypeDef core.EnumTypeDefID
}) (*core.TypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	enum, err := args.EnumTypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode enum type def: %w", err)
	}
	return def.WithEnumTypeDef(enum), nil
}

func supportEnumMembers(ctx context.Context) bool {
	return core.Supports(ctx, "v0.18.11")
}

func (s *moduleSchema) generatedCode(ctx context.Context, _ *core.Query, args struct {
	Code core.DirectoryID
}) (*core.GeneratedCode, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	dir, err := args.Code.Load(ctx, dag)
	if err != nil {
		return nil, err
	}
	return core.NewGeneratedCode(dir), nil
}

func (s *moduleSchema) module(ctx context.Context, query *core.Query, _ struct{}) (*core.Module, error) {
	return query.NewModule(), nil
}

func (s *moduleSchema) function(ctx context.Context, _ *core.Query, args struct {
	Name             string
	ReturnType       core.TypeDefID
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.Function, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	returnType, err := args.ReturnType.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode return type: %w", err)
	}
	fn := core.NewFunction(args.Name, returnType)
	if args.SourceModuleName.Valid {
		fn.SourceModuleName = string(args.SourceModuleName.Value)
	}
	return fn, nil
}

func (s *moduleSchema) internalFunction(ctx context.Context, _ *core.Query, args struct {
	Name             string
	ReturnType       core.TypeDefID
	SourceModuleName dagql.Optional[dagql.String] `internal:"true"`
}) (*core.Function, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	returnType, err := args.ReturnType.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode return type: %w", err)
	}
	fn := &core.Function{
		Name:         args.Name,
		OriginalName: args.Name,
		ReturnType:   returnType,
	}
	if args.SourceModuleName.Valid {
		fn.SourceModuleName = string(args.SourceModuleName.Value)
	}
	return fn, nil
}

func (s *moduleSchema) sourceMap(ctx context.Context, _ *core.Query, args struct {
	Module   dagql.Optional[dagql.String] `internal:"true"`
	Filename string
	Line     int
	Column   int
	URL      dagql.Optional[dagql.String] `internal:"true"`
}) (*core.SourceMap, error) {
	var module string
	if args.Module.Valid {
		module = string(args.Module.Value)
	}
	var url string
	if args.URL.Valid {
		url = string(args.URL.Value)
	}
	return &core.SourceMap{
		Module:   module,
		Filename: args.Filename,
		Line:     args.Line,
		Column:   args.Column,
		URL:      url,
	}, nil
}

func (s *moduleSchema) functionWithDescription(ctx context.Context, fn *core.Function, args struct {
	Description string
}) (*core.Function, error) {
	return fn.WithDescription(args.Description), nil
}

func (s *moduleSchema) functionWithDeprecated(ctx context.Context, fn *core.Function, args struct {
	Reason *string
}) (*core.Function, error) {
	return fn.WithDeprecated(args.Reason), nil
}

func (s *moduleSchema) functionWithCheck(ctx context.Context, fn *core.Function, args struct{}) (*core.Function, error) {
	return fn.WithCheck(), nil
}

func (s *moduleSchema) functionWithGenerator(ctx context.Context, fn *core.Function, args struct{}) (*core.Function, error) {
	return fn.WithGenerator(), nil
}

func (s *moduleSchema) functionAsConstructor(ctx context.Context, fn *core.Function, _ struct{}) (*core.Function, error) {
	fn = fn.Clone()
	fn.Name = ""
	fn.OriginalName = ""
	return fn, nil
}

func (s *moduleSchema) functionWithUp(ctx context.Context, fn *core.Function, args struct{}) (*core.Function, error) {
	return fn.WithUp(), nil
}

func (s *moduleSchema) functionWithArg(ctx context.Context, fn *core.Function, args struct {
	Name           string
	TypeDef        core.TypeDefID
	Description    string    `default:""`
	DefaultValue   core.JSON `default:""`
	DefaultPath    string    `default:""`
	DefaultAddress string    `default:""`
	Ignore         []string  `default:"[]"`
	SourceMap      dagql.Optional[core.SourceMapID]
	Deprecated     *string
}) (*core.Function, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	argType, err := args.TypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode arg type: %w", err)
	}
	argTypeID, err := argType.ID()
	if err != nil {
		return nil, fmt.Errorf("failed to get arg type id: %w", err)
	}
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}

	// Check if multiple default values are used, return an error if so.
	defaultSet := []bool{
		args.DefaultValue != nil,
		args.DefaultPath != "",
		args.DefaultAddress != "",
	}
	defaultCount := 0
	for _, v := range defaultSet {
		if v {
			defaultCount++
		}
	}
	if defaultCount > 1 {
		return nil, fmt.Errorf("cannot set more than one default value")
	}

	// Check if default path from context is set for supported type
	if args.DefaultPath != "" {
		if argType.Self().Kind != core.TypeDefKindObject {
			return nil, fmt.Errorf("can only set default path for Object, not %s", argType.Self().Kind)
		}
		name := argType.Self().AsObject.Value.Self().Name
		if !slices.Contains([]string{"Directory", "File", "GitRepository", "GitRef"}, name) {
			return nil, fmt.Errorf("cannot set default path for %s", name)
		}
	}

	// Check if default address is set for supported type (Container only)
	if args.DefaultAddress != "" {
		if argType.Self().Kind != core.TypeDefKindObject {
			return nil, fmt.Errorf("can only set default address for Object, not %s", argType.Self().Kind)
		}
		name := argType.Self().AsObject.Value.Self().Name
		if name != "Container" {
			return nil, fmt.Errorf("can only set default address for Container type, not %s", name)
		}
	}

	// Check if ignore is set for non-directory type
	if len(args.Ignore) > 0 {
		if argType.Self().Kind != core.TypeDefKindObject {
			return nil, fmt.Errorf("can only set ignore for Object type, not %s", argType.Self().Kind)
		}
		name := argType.Self().AsObject.Value.Self().Name
		if name != "Directory" {
			return nil, fmt.Errorf("can only set ignore for Directory type, not %s", name)
		}
	}

	// When using a default path or address, SDKs can't set a default value and the argument
	// may be non-nullable, so we need to enforce it as optional.
	var arg dagql.ObjectResult[*core.FunctionArg]
	if err := dag.Select(ctx, dag.Root(), &arg, dagql.Selector{
		Field: "__functionArg",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(args.Name)},
			{Name: "typeDef", Value: dagql.NewID[*core.TypeDef](argTypeID)},
			{Name: "description", Value: dagql.String(args.Description)},
			{Name: "defaultValue", Value: args.DefaultValue},
			{Name: "defaultPath", Value: dagql.String(args.DefaultPath)},
			{Name: "defaultAddress", Value: dagql.String(args.DefaultAddress)},
			{Name: "ignore", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(args.Ignore...))},
			{Name: "sourceMap", Value: optID(sourceMap)},
			{Name: "deprecated", Value: optString(args.Deprecated)},
		},
	}); err != nil {
		return nil, err
	}
	return fn.WithArg(arg), nil
}

func (s *moduleSchema) functionWithArgResult(ctx context.Context, fn *core.Function, args struct {
	Arg core.FunctionArgID
}) (*core.Function, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	arg, err := args.Arg.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode function arg: %w", err)
	}
	return fn.WithArg(arg), nil
}

func (s *moduleSchema) functionWithReturnType(ctx context.Context, fn *core.Function, args struct {
	ReturnType core.TypeDefID
}) (*core.Function, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	returnType, err := args.ReturnType.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode return type: %w", err)
	}
	return fn.WithReturnType(returnType), nil
}

func (s *moduleSchema) functionWithSourceMap(ctx context.Context, fn *core.Function, args struct {
	SourceMap core.SourceMapID
}) (*core.Function, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	sourceMap, err := args.SourceMap.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode source map: %w", err)
	}
	return fn.WithSourceMap(sourceMap), nil
}

func (s *moduleSchema) functionWithSourceMapResult(ctx context.Context, fn *core.Function, args struct {
	SourceMap dagql.Optional[core.SourceMapID]
}) (*core.Function, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return fn.WithSourceMap(sourceMap), nil
}

func (s *moduleSchema) functionArgWithTypeDef(ctx context.Context, arg *core.FunctionArg, args struct {
	TypeDef core.TypeDefID
}) (*core.FunctionArg, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	typeDef, err := args.TypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode arg type: %w", err)
	}
	return arg.WithTypeDef(typeDef), nil
}

func (s *moduleSchema) functionArgWithSourceMap(ctx context.Context, arg *core.FunctionArg, args struct {
	SourceMap dagql.Optional[core.SourceMapID]
}) (*core.FunctionArg, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return arg.WithSourceMap(sourceMap), nil
}

func (s *moduleSchema) functionArgWithDefaultValue(ctx context.Context, arg *core.FunctionArg, args struct {
	DefaultValue core.JSON `default:""`
}) (*core.FunctionArg, error) {
	return arg.WithDefaultValue(args.DefaultValue), nil
}

func (s *moduleSchema) functionArgWithDefaultPath(ctx context.Context, arg *core.FunctionArg, args struct {
	DefaultPath string `default:""`
}) (*core.FunctionArg, error) {
	return arg.WithDefaultPath(args.DefaultPath), nil
}

func (s *moduleSchema) functionArgWithDefaultAddress(ctx context.Context, arg *core.FunctionArg, args struct {
	DefaultAddress string `default:""`
}) (*core.FunctionArg, error) {
	return arg.WithDefaultAddress(args.DefaultAddress), nil
}

func (s *moduleSchema) functionArgWithIgnore(ctx context.Context, arg *core.FunctionArg, args struct {
	Ignore []string `default:"[]"`
}) (*core.FunctionArg, error) {
	return arg.WithIgnore(args.Ignore), nil
}

func (s *moduleSchema) functionWithCachePolicy(
	ctx context.Context,
	fn *core.Function,
	args struct {
		Policy     core.FunctionCachePolicy
		TimeToLive dagql.Optional[dagql.String]
	},
) (*core.Function, error) {
	fn = fn.Clone()

	fn.CachePolicy = args.Policy

	if args.TimeToLive.Valid {
		// For now, restrict TTLs to the default policy. We could support it
		// for PerSession in the future if desired.
		if fn.CachePolicy != core.FunctionCachePolicyDefault {
			return nil, errors.New("time to live can only be set with default cache policy")
		}

		ttlDuration, err := time.ParseDuration(string(args.TimeToLive.Value))
		if err != nil {
			return nil, fmt.Errorf("failed to parse time to live duration %q: %w", args.TimeToLive.Value, err)
		}

		switch {
		case ttlDuration == 0:
			// a TTL of 0 sounds an awful lot like "never cache", so we treat it that way.
			fn.CachePolicy = core.FunctionCachePolicyNever

		case ttlDuration < core.MinFunctionCacheTTLSeconds*time.Second:
			return nil, fmt.Errorf("time to live duration must be at least %q, got %q",
				(core.MinFunctionCacheTTLSeconds * time.Second).String(),
				args.TimeToLive.Value,
			)

		case ttlDuration > core.MaxFunctionCacheTTLSeconds*time.Second:
			return nil, fmt.Errorf("time to live duration must be at most %q, got %q",
				(core.MaxFunctionCacheTTLSeconds * time.Second).String(),
				args.TimeToLive.Value,
			)

		default:
			fn.CacheTTLSeconds = dagql.NonNull(dagql.Int(int(ttlDuration.Seconds())))
		}
	}

	return fn, nil
}

func (s *moduleSchema) objectTypeDefWithField(ctx context.Context, obj *core.ObjectTypeDef, args struct {
	Field core.FieldTypeDefID
}) (*core.ObjectTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	field, err := args.Field.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode field: %w", err)
	}
	return obj.WithField(field), nil
}

func (s *moduleSchema) objectTypeDefWithName(ctx context.Context, obj *core.ObjectTypeDef, args struct {
	Name string
}) (*core.ObjectTypeDef, error) {
	return obj.WithName(args.Name), nil
}

func (s *moduleSchema) objectTypeDefWithSourceMap(ctx context.Context, obj *core.ObjectTypeDef, args struct {
	SourceMap dagql.Optional[core.SourceMapID]
}) (*core.ObjectTypeDef, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return obj.WithSourceMap(sourceMap), nil
}

func (s *moduleSchema) objectTypeDefWithSourceModuleName(ctx context.Context, obj *core.ObjectTypeDef, args struct {
	SourceModuleName dagql.Optional[dagql.String]
}) (*core.ObjectTypeDef, error) {
	if !args.SourceModuleName.Valid {
		return obj.WithSourceModuleName(""), nil
	}
	return obj.WithSourceModuleName(string(args.SourceModuleName.Value)), nil
}

func (s *moduleSchema) objectTypeDefWithFunction(ctx context.Context, obj *core.ObjectTypeDef, args struct {
	Function core.FunctionID
}) (*core.ObjectTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	fn, err := args.Function.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode function: %w", err)
	}
	return obj.WithFunction(fn), nil
}

func (s *moduleSchema) objectTypeDefWithConstructor(ctx context.Context, obj *core.ObjectTypeDef, args struct {
	Function core.FunctionID
}) (*core.ObjectTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	fn, err := args.Function.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode constructor function: %w", err)
	}
	return obj.WithConstructor(fn), nil
}

func (s *moduleSchema) interfaceTypeDefWithFunction(ctx context.Context, iface *core.InterfaceTypeDef, args struct {
	Function core.FunctionID
}) (*core.InterfaceTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	fn, err := args.Function.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode function: %w", err)
	}
	return iface.WithFunction(fn), nil
}

func (s *moduleSchema) interfaceTypeDefWithName(ctx context.Context, iface *core.InterfaceTypeDef, args struct {
	Name string
}) (*core.InterfaceTypeDef, error) {
	return iface.WithName(args.Name), nil
}

func (s *moduleSchema) interfaceTypeDefWithSourceMap(ctx context.Context, iface *core.InterfaceTypeDef, args struct {
	SourceMap dagql.Optional[core.SourceMapID]
}) (*core.InterfaceTypeDef, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return iface.WithSourceMap(sourceMap), nil
}

func (s *moduleSchema) interfaceTypeDefWithSourceModuleName(ctx context.Context, iface *core.InterfaceTypeDef, args struct {
	SourceModuleName dagql.Optional[dagql.String]
}) (*core.InterfaceTypeDef, error) {
	if !args.SourceModuleName.Valid {
		return iface.WithSourceModuleName(""), nil
	}
	return iface.WithSourceModuleName(string(args.SourceModuleName.Value)), nil
}

func (s *moduleSchema) inputTypeDefWithField(ctx context.Context, input *core.InputTypeDef, args struct {
	Field core.FieldTypeDefID
}) (*core.InputTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	field, err := args.Field.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode field: %w", err)
	}
	return input.WithField(field), nil
}

func (s *moduleSchema) fieldTypeDefWithTypeDef(ctx context.Context, field *core.FieldTypeDef, args struct {
	TypeDef core.TypeDefID
}) (*core.FieldTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	typeDef, err := args.TypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode field type: %w", err)
	}
	return field.WithTypeDef(typeDef), nil
}

func (s *moduleSchema) fieldTypeDefWithSourceMap(ctx context.Context, field *core.FieldTypeDef, args struct {
	SourceMap dagql.Optional[core.SourceMapID]
}) (*core.FieldTypeDef, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return field.WithSourceMap(sourceMap), nil
}

func (s *moduleSchema) listTypeDefWithElementTypeDef(ctx context.Context, list *core.ListTypeDef, args struct {
	ElementTypeDef core.TypeDefID
}) (*core.ListTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	elementTypeDef, err := args.ElementTypeDef.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode element type: %w", err)
	}
	return list.WithElementTypeDef(elementTypeDef), nil
}

func (s *moduleSchema) enumTypeDefWithMember(ctx context.Context, enum *core.EnumTypeDef, args struct {
	Member core.EnumMemberTypeDefID
}) (*core.EnumTypeDef, error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	member, err := args.Member.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to decode member: %w", err)
	}
	return enum.WithMember(member)
}

func (s *moduleSchema) enumTypeDefWithName(ctx context.Context, enum *core.EnumTypeDef, args struct {
	Name string
}) (*core.EnumTypeDef, error) {
	return enum.WithName(args.Name), nil
}

func (s *moduleSchema) enumTypeDefWithSourceMap(ctx context.Context, enum *core.EnumTypeDef, args struct {
	SourceMap dagql.Optional[core.SourceMapID]
}) (*core.EnumTypeDef, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return enum.WithSourceMap(sourceMap), nil
}

func (s *moduleSchema) enumTypeDefWithSourceModuleName(ctx context.Context, enum *core.EnumTypeDef, args struct {
	SourceModuleName dagql.Optional[dagql.String]
}) (*core.EnumTypeDef, error) {
	if !args.SourceModuleName.Valid {
		return enum.WithSourceModuleName(""), nil
	}
	return enum.WithSourceModuleName(string(args.SourceModuleName.Value)), nil
}

func (s *moduleSchema) enumMemberTypeDefWithName(ctx context.Context, member *core.EnumMemberTypeDef, args struct {
	Name string
}) (*core.EnumMemberTypeDef, error) {
	return member.WithName(args.Name), nil
}

func (s *moduleSchema) enumMemberTypeDefWithSourceMap(ctx context.Context, member *core.EnumMemberTypeDef, args struct {
	SourceMap dagql.Optional[core.SourceMapID]
}) (*core.EnumMemberTypeDef, error) {
	sourceMap, err := s.loadSourceMapResult(ctx, args.SourceMap)
	if err != nil {
		return nil, err
	}
	return member.WithSourceMap(sourceMap), nil
}

type currentModuleArgs struct {
	ImplementationScopedMod dagql.Optional[core.ModuleID] `internal:"true"`
}

func (s *moduleSchema) currentModuleCacheKey(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Query],
	args currentModuleArgs,
	req *dagql.CallRequest,
) error {
	if args.ImplementationScopedMod.Valid {
		return nil
	}

	mod, err := parent.Self().CurrentModule(ctx)
	if err != nil {
		return fmt.Errorf("failed to get current module: %w", err)
	}
	scopedMod, err := core.ImplementationScopedModule(ctx, mod)
	if err != nil {
		return fmt.Errorf("failed to get implementation-scoped current module: %w", err)
	}
	scopedModID, err := scopedMod.ID()
	if err != nil {
		return fmt.Errorf("failed to get implementation-scoped current module ID: %w", err)
	}
	args.ImplementationScopedMod = dagql.Opt(dagql.NewID[*core.Module](scopedModID))
	return req.SetArgInput(ctx, "implementationScopedMod", dagql.NewID[*core.Module](scopedModID), false)
}

func (s *moduleSchema) currentModule(
	ctx context.Context,
	self *core.Query,
	args currentModuleArgs,
) (*core.CurrentModule, error) {
	if !args.ImplementationScopedMod.Valid {
		return nil, errors.New("missing source module argument")
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}
	mod, err := args.ImplementationScopedMod.Value.Load(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to load implementation-scoped module: %w", err)
	}
	return &core.CurrentModule{Module: mod}, nil
}

func (s *moduleSchema) currentFunctionCall(ctx context.Context, self *core.Query, _ struct{}) (*core.FunctionCall, error) {
	return self.CurrentFunctionCall(ctx)
}

func (s *moduleSchema) moduleRuntime(ctx context.Context, mod *core.Module, _ struct{}) (dagql.Nullable[dagql.ObjectResult[*core.Container]], error) {
	if mod.Runtime.Valid {
		return mod.Runtime, nil
	}

	runtime, err := mod.LoadRuntime(ctx)
	if err != nil {
		return dagql.Nullable[dagql.ObjectResult[*core.Container]]{}, err
	}
	if ctr, ok := runtime.AsContainer(); ok {
		return dagql.NonNull(ctr), nil
	}
	return dagql.Nullable[dagql.ObjectResult[*core.Container]]{}, nil
}

func (s *moduleSchema) moduleServe(ctx context.Context, modMeta dagql.ObjectResult[*core.Module], args struct {
	IncludeDependencies dagql.Optional[dagql.Boolean]
	Entrypoint          dagql.Optional[dagql.Boolean]
}) (dagql.Nullable[core.Void], error) {
	void := dagql.Null[core.Void]()

	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return void, err
	}

	includeDependencies := args.IncludeDependencies.Valid && args.IncludeDependencies.Value.Bool()
	entrypoint := args.Entrypoint.Valid && args.Entrypoint.Value.Bool()
	return void, query.ServeModule(ctx, modMeta, includeDependencies, entrypoint)
}

type currentTypeDefsArgs struct {
	ReturnAllTypes bool `default:"false"`
	HideCore       dagql.Optional[dagql.Boolean]
}

func (s *moduleSchema) currentTypeDefs(ctx context.Context, self *core.Query, args currentTypeDefsArgs) (dagql.ObjectResultArray[*core.TypeDef], error) {
	deps, err := self.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current module: %w", err)
	}
	dag, err := deps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current schema: %w", err)
	}
	typeDefs, err := deps.TypeDefs(ctx, dag)
	if err != nil {
		return nil, err
	}

	queryTypeDef, err := currentQueryTypeDef(ctx, dag)
	if err != nil {
		return nil, fmt.Errorf("failed to load live query typedef: %w", err)
	}
	queryReplaced := false
	for i, typeDef := range typeDefs {
		if typeDef.Self() == nil || typeDef.Self().Kind != core.TypeDefKindObject {
			continue
		}
		if !typeDef.Self().AsObject.Valid || typeDef.Self().AsObject.Value.Self() == nil {
			continue
		}
		if typeDef.Self().AsObject.Value.Self().Name != "Query" {
			continue
		}
		typeDefs[i] = queryTypeDef
		queryReplaced = true
		break
	}
	if !queryReplaced {
		typeDefs = append(typeDefs, queryTypeDef)
	}

	if args.HideCore.GetOr(dagql.NewBoolean(false)).Bool() {
		typeDefs, err = stripCoreQueryFunctions(ctx, dag, typeDefs)
		if err != nil {
			return nil, err
		}
	}
	if !args.ReturnAllTypes {
		return typeDefs, nil
	}
	return expandTypeDefClosure(ctx, dag, typeDefs)
}

func stripCoreQueryFunctions(
	ctx context.Context,
	dag *dagql.Server,
	typeDefs dagql.ObjectResultArray[*core.TypeDef],
) (dagql.ObjectResultArray[*core.TypeDef], error) {
	result := make(dagql.ObjectResultArray[*core.TypeDef], 0, len(typeDefs))
	for _, typeDef := range typeDefs {
		typeDefSelf := typeDef.Self()
		if typeDefSelf == nil || typeDefSelf.Kind != core.TypeDefKindObject || !typeDefSelf.AsObject.Valid || typeDefSelf.AsObject.Value.Self() == nil || typeDefSelf.AsObject.Value.Self().Name != "Query" {
			result = append(result, typeDef)
			continue
		}

		queryObj := typeDefSelf.AsObject.Value
		queryObjSelf := queryObj.Self()

		var sourceMap dagql.ObjectResult[*core.SourceMap]
		if queryObjSelf.SourceMap.Valid {
			sourceMap = queryObjSelf.SourceMap.Value
		}

		var filteredObj dagql.ObjectResult[*core.ObjectTypeDef]
		if err := dag.Select(ctx, dag.Root(), &filteredObj, dagql.Selector{
			Field: "__objectTypeDef",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(queryObjSelf.Name)},
				{Name: "description", Value: dagql.String(queryObjSelf.Description)},
				{Name: "sourceMap", Value: optID(sourceMap)},
				{Name: "deprecated", Value: optString(queryObjSelf.Deprecated)},
				{Name: "sourceModuleName", Value: core.OptSourceModuleName(queryObjSelf.SourceModuleName)},
			},
		}); err != nil {
			return nil, fmt.Errorf("create filtered Query object typedef: %w", err)
		}

		for _, field := range queryObjSelf.Fields {
			if field.Self() == nil {
				continue
			}
			fieldID, err := core.ResultIDInput(field)
			if err != nil {
				return nil, err
			}
			if err := dag.Select(ctx, filteredObj, &filteredObj, dagql.Selector{
				Field: "__withField",
				Args:  []dagql.NamedInput{{Name: "field", Value: fieldID}},
			}); err != nil {
				return nil, fmt.Errorf("add Query field %q to filtered typedef: %w", field.Self().Name, err)
			}
		}

		for _, fn := range queryObjSelf.Functions {
			if fn.Self() == nil {
				continue
			}
			if fn.Self().SourceModuleName == "" && fn.Self().Name != "with" {
				continue
			}
			fnID, err := core.ResultIDInput(fn)
			if err != nil {
				return nil, err
			}
			if err := dag.Select(ctx, filteredObj, &filteredObj, dagql.Selector{
				Field: "__withFunction",
				Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
			}); err != nil {
				return nil, fmt.Errorf("add Query function %q to filtered typedef: %w", fn.Self().Name, err)
			}
		}

		objID, err := core.ResultIDInput(filteredObj)
		if err != nil {
			return nil, err
		}
		filteredTypeDef, err := core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
			Field: "__withObjectTypeDef",
			Args:  []dagql.NamedInput{{Name: "objectTypeDef", Value: objID}},
		})
		if err != nil {
			return nil, fmt.Errorf("wrap filtered Query typedef: %w", err)
		}

		result = append(result, filteredTypeDef)
	}
	return result, nil
}

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func expandTypeDefClosure(
	ctx context.Context,
	dag *dagql.Server,
	typeDefs dagql.ObjectResultArray[*core.TypeDef],
) (dagql.ObjectResultArray[*core.TypeDef], error) {
	orderedNames := make([]string, 0, len(typeDefs))
	canonicalByName := make(map[string]dagql.ObjectResult[*core.TypeDef], len(typeDefs))
	queue := make([]dagql.ObjectResult[*core.TypeDef], 0, len(typeDefs))

	enqueue := func(typeDef dagql.ObjectResult[*core.TypeDef]) error {
		if typeDef.Self() == nil {
			return nil
		}
		typeDef, err := normalizeReturnAllTypesTypeDef(ctx, dag, typeDef)
		if err != nil {
			return err
		}
		typeDefSelf := typeDef.Self()
		if typeDefSelf == nil {
			return nil
		}
		if typeDefSelf.Name == "" {
			return fmt.Errorf("typedef %q missing canonical name", typeDefSelf.Kind)
		}

		if existing, found := canonicalByName[typeDefSelf.Name]; !found {
			orderedNames = append(orderedNames, typeDefSelf.Name)
			canonicalByName[typeDefSelf.Name] = typeDef
			queue = append(queue, typeDef)
		} else if typeDefIsStub(existing.Self()) && !typeDefIsStub(typeDefSelf) {
			canonicalByName[typeDefSelf.Name] = typeDef
			queue = append(queue, typeDef)
		}
		return nil
	}

	for _, typeDef := range typeDefs {
		if err := enqueue(typeDef); err != nil {
			return nil, err
		}
	}

	for len(queue) > 0 {
		typeDef := queue[0]
		queue = queue[1:]
		typeDefSelf := typeDef.Self()
		if typeDefSelf == nil {
			continue
		}

		switch typeDefSelf.Kind {
		case core.TypeDefKindList:
			if typeDefSelf.AsList.Valid && typeDefSelf.AsList.Value.Self() != nil {
				if err := enqueue(typeDefSelf.AsList.Value.Self().ElementTypeDef); err != nil {
					return nil, err
				}
			}
		case core.TypeDefKindObject:
			if !typeDefSelf.AsObject.Valid || typeDefSelf.AsObject.Value.Self() == nil {
				continue
			}
			obj := typeDefSelf.AsObject.Value.Self()
			for _, field := range obj.Fields {
				if field.Self() == nil {
					continue
				}
				if err := enqueue(field.Self().TypeDef); err != nil {
					return nil, err
				}
			}
			for _, fn := range obj.Functions {
				if fn.Self() == nil {
					continue
				}
				if err := enqueue(fn.Self().ReturnType); err != nil {
					return nil, err
				}
				for _, arg := range fn.Self().Args {
					if arg.Self() == nil {
						continue
					}
					if err := enqueue(arg.Self().TypeDef); err != nil {
						return nil, err
					}
				}
			}
			if obj.Constructor.Valid && obj.Constructor.Value.Self() != nil {
				if err := enqueue(obj.Constructor.Value.Self().ReturnType); err != nil {
					return nil, err
				}
				for _, arg := range obj.Constructor.Value.Self().Args {
					if arg.Self() == nil {
						continue
					}
					if err := enqueue(arg.Self().TypeDef); err != nil {
						return nil, err
					}
				}
			}
		case core.TypeDefKindInterface:
			if !typeDefSelf.AsInterface.Valid || typeDefSelf.AsInterface.Value.Self() == nil {
				continue
			}
			iface := typeDefSelf.AsInterface.Value.Self()
			for _, fn := range iface.Functions {
				if fn.Self() == nil {
					continue
				}
				if err := enqueue(fn.Self().ReturnType); err != nil {
					return nil, err
				}
				for _, arg := range fn.Self().Args {
					if arg.Self() == nil {
						continue
					}
					if err := enqueue(arg.Self().TypeDef); err != nil {
						return nil, err
					}
				}
			}
		case core.TypeDefKindInput:
			if !typeDefSelf.AsInput.Valid || typeDefSelf.AsInput.Value.Self() == nil {
				continue
			}
			input := typeDefSelf.AsInput.Value.Self()
			for _, field := range input.Fields {
				if field.Self() == nil {
					continue
				}
				if err := enqueue(field.Self().TypeDef); err != nil {
					return nil, err
				}
			}
		}
	}

	ordered := make(dagql.ObjectResultArray[*core.TypeDef], 0, len(orderedNames))
	for _, name := range orderedNames {
		ordered = append(ordered, canonicalByName[name])
	}
	return ordered, nil
}

func normalizeReturnAllTypesTypeDef(
	ctx context.Context,
	dag *dagql.Server,
	typeDef dagql.ObjectResult[*core.TypeDef],
) (dagql.ObjectResult[*core.TypeDef], error) {
	if typeDef.Self() == nil || !typeDef.Self().Optional {
		return typeDef, nil
	}
	if err := dag.Select(ctx, typeDef, &typeDef, dagql.Selector{
		Field: "withOptional",
		Args:  []dagql.NamedInput{{Name: "optional", Value: dagql.Boolean(false)}},
	}); err != nil {
		return typeDef, fmt.Errorf("normalize typedef optional=false: %w", err)
	}
	return typeDef, nil
}

func typeDefIsStub(typeDef *core.TypeDef) bool {
	if typeDef == nil {
		return false
	}
	switch typeDef.Kind {
	case core.TypeDefKindObject:
		if !typeDef.AsObject.Valid || typeDef.AsObject.Value.Self() == nil {
			return true
		}
		obj := typeDef.AsObject.Value.Self()
		return len(obj.Fields) == 0 && len(obj.Functions) == 0 && !obj.Constructor.Valid
	case core.TypeDefKindInterface:
		if !typeDef.AsInterface.Valid || typeDef.AsInterface.Value.Self() == nil {
			return true
		}
		return len(typeDef.AsInterface.Value.Self().Functions) == 0
	case core.TypeDefKindInput:
		if !typeDef.AsInput.Valid || typeDef.AsInput.Value.Self() == nil {
			return true
		}
		return len(typeDef.AsInput.Value.Self().Fields) == 0
	case core.TypeDefKindEnum:
		if !typeDef.AsEnum.Valid || typeDef.AsEnum.Value.Self() == nil {
			return true
		}
		return len(typeDef.AsEnum.Value.Self().Members) == 0
	default:
		return false
	}
}

//nolint:gocyclo // intrinsically long state machine; refactoring would hurt clarity
func currentQueryTypeDef(ctx context.Context, dag *dagql.Server) (dagql.ObjectResult[*core.TypeDef], error) {
	dagqlSchema := dagqlintrospection.WrapSchema(dag.Schema())
	queryType := dagqlSchema.QueryType()
	codeGenType, err := core.DagqlToCodegenType(queryType)
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, err
	}
	queryObjType := dag.Root().ObjectType()

	var obj dagql.ObjectResult[*core.ObjectTypeDef]
	if err := dag.Select(ctx, dag.Root(), &obj, dagql.Selector{
		Field: "__objectTypeDef",
		Args: []dagql.NamedInput{
			{Name: "name", Value: dagql.String(codeGenType.Name)},
			{Name: "description", Value: dagql.String(codeGenType.Description)},
		},
	}); err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, err
	}

	for _, introspectionField := range codeGenType.Fields {
		rtType, ok, err := introspectionRefToTypeDef(ctx, dag, introspectionField.TypeRef, false, false)
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("failed to convert return type: %w", err)
		}
		if !ok {
			continue
		}
		rtTypeID, err := core.ResultIDInput(rtType)
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, err
		}
		var sourceModuleName dagql.Optional[dagql.String]
		if fieldSpec, ok := queryObjType.FieldSpec(introspectionField.Name, dag.View); ok {
			if fieldSpec.Module != nil && fieldSpec.Module.ResultRef != nil {
				sourceModuleName = core.OptSourceModuleName(fieldSpec.Module.Name)
			}
		}
		var fn dagql.ObjectResult[*core.Function]
		if err := dag.Select(ctx, dag.Root(), &fn, dagql.Selector{
			Field: "__function",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(introspectionField.Name)},
				{Name: "returnType", Value: rtTypeID},
				{Name: "sourceModuleName", Value: sourceModuleName},
			},
		}); err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, err
		}
		if introspectionField.Description != "" {
			if err := dag.Select(ctx, fn, &fn, dagql.Selector{
				Field: "withDescription",
				Args:  []dagql.NamedInput{{Name: "description", Value: dagql.String(introspectionField.Description)}},
			}); err != nil {
				return dagql.ObjectResult[*core.TypeDef]{}, err
			}
		}
		if introspectionField.DeprecationReason != nil {
			if err := dag.Select(ctx, fn, &fn, dagql.Selector{
				Field: "withDeprecated",
				Args:  []dagql.NamedInput{{Name: "reason", Value: core.OptString(introspectionField.DeprecationReason)}},
			}); err != nil {
				return dagql.ObjectResult[*core.TypeDef]{}, err
			}
		}

		for _, introspectionArg := range introspectionField.Args {
			argType, ok, err := introspectionRefToTypeDef(ctx, dag, introspectionArg.TypeRef, false, true)
			if err != nil {
				return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("failed to convert argument type: %w", err)
			}
			if !ok {
				continue
			}
			argTypeID, err := core.ResultIDInput(argType)
			if err != nil {
				return dagql.ObjectResult[*core.TypeDef]{}, err
			}
			var (
				defaultPath    string
				defaultAddress string
				ignore         []string
				resolvedSpec   dagql.InputSpec
			)
			if fieldSpec, ok := queryObjType.FieldSpec(introspectionField.Name, dag.View); ok {
				if argSpec, ok := fieldSpec.Args.Input(introspectionArg.Name, dag.View); ok {
					resolvedSpec = argSpec
					for _, directive := range argSpec.Directives {
						switch directive.Name {
						case "defaultPath":
							if arg := directive.Arguments.ForName("path"); arg != nil && arg.Value != nil {
								defaultPath = arg.Value.Raw
							}
						case "defaultAddress":
							if arg := directive.Arguments.ForName("address"); arg != nil && arg.Value != nil {
								defaultAddress = arg.Value.Raw
							}
						case "ignorePatterns":
							if arg := directive.Arguments.ForName("patterns"); arg != nil && arg.Value != nil && arg.Value.Kind == ast.ListValue {
								for _, child := range arg.Value.Children {
									if child != nil && child.Value != nil {
										ignore = append(ignore, child.Value.Raw)
									}
								}
							}
						}
					}
				}
			}
			defaultValue, err := introspectionDefaultToJSON(introspectionArg.DefaultValue, resolvedSpec)
			if err != nil {
				return dagql.ObjectResult[*core.TypeDef]{}, fmt.Errorf("convert default value for arg %q: %w", introspectionArg.Name, err)
			}
			var fnArg dagql.ObjectResult[*core.FunctionArg]
			if err := dag.Select(ctx, dag.Root(), &fnArg, dagql.Selector{
				Field: "__functionArgExact",
				Args: []dagql.NamedInput{
					{Name: "name", Value: dagql.String(introspectionArg.Name)},
					{Name: "typeDef", Value: argTypeID},
					{Name: "description", Value: dagql.String(introspectionArg.Description)},
					{Name: "defaultValue", Value: defaultValue},
					{Name: "defaultPath", Value: dagql.String(defaultPath)},
					{Name: "defaultAddress", Value: dagql.String(defaultAddress)},
					{Name: "ignore", Value: dagql.ArrayInput[dagql.String](dagql.NewStringArray(ignore...))},
					{Name: "deprecated", Value: core.OptString(introspectionArg.DeprecationReason)},
				},
			}); err != nil {
				return dagql.ObjectResult[*core.TypeDef]{}, err
			}
			fnArgID, err := core.ResultIDInput(fnArg)
			if err != nil {
				return dagql.ObjectResult[*core.TypeDef]{}, err
			}
			if err := dag.Select(ctx, fn, &fn, dagql.Selector{
				Field: "__withArg",
				Args:  []dagql.NamedInput{{Name: "arg", Value: fnArgID}},
			}); err != nil {
				return dagql.ObjectResult[*core.TypeDef]{}, err
			}
		}

		fnID, err := core.ResultIDInput(fn)
		if err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, err
		}
		if err := dag.Select(ctx, obj, &obj, dagql.Selector{
			Field: "__withFunction",
			Args:  []dagql.NamedInput{{Name: "function", Value: fnID}},
		}); err != nil {
			return dagql.ObjectResult[*core.TypeDef]{}, err
		}
	}

	objID, err := core.ResultIDInput(obj)
	if err != nil {
		return dagql.ObjectResult[*core.TypeDef]{}, err
	}
	return core.SelectTypeDefWithServer(ctx, dag, dagql.Selector{
		Field: "__withObjectTypeDef",
		Args:  []dagql.NamedInput{{Name: "objectTypeDef", Value: objID}},
	})
}

func (s *moduleSchema) functionCallReturnValue(ctx context.Context, fnCall *core.FunctionCall, args struct {
	Value core.JSON
},
) (dagql.Nullable[core.Void], error) {
	// TODO: error out if caller is not coming from a module
	return dagql.Null[core.Void](), fnCall.ReturnValue(ctx, args.Value)
}

func (s *moduleSchema) functionCallReturnError(ctx context.Context, fnCall *core.FunctionCall, args struct {
	Error dagql.ID[*core.Error]
},
) (dagql.Nullable[core.Void], error) {
	// TODO: error out if caller is not coming from a module
	return dagql.Null[core.Void](), fnCall.ReturnError(ctx, args.Error)
}

func (s *moduleSchema) moduleGeneratedContextDirectory(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	args struct{},
) (inst dagql.Result[*core.Directory], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	err = dag.Select(ctx, mod.Self().Source.Value, &inst,
		dagql.Selector{
			Field: "generatedContextDirectory",
		},
	)
	return inst, err
}

func (s *moduleSchema) moduleUserDefaults(ctx context.Context, mod *core.Module, _ struct{}) (*core.EnvFile, error) {
	return mod.UserDefaults(ctx)
}

func (s *moduleSchema) moduleIntrospectionSchemaJSON(
	ctx context.Context,
	mod *core.Module,
	args struct{},
) (dagql.Result[*core.File], error) {
	return mod.Deps.SchemaIntrospectionJSONFileForModule(ctx)
}

func (s *moduleSchema) moduleChecks(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	args struct {
		Include    dagql.Optional[dagql.ArrayInput[dagql.String]]
		NoGenerate dagql.Optional[dagql.Boolean]
	},
) (*core.CheckGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}
	return core.NewCheckGroup(ctx, mod, include, args.NoGenerate.GetOr(false).Bool())
}

func (s *moduleSchema) moduleCheck(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	args struct {
		Name string
	},
) (*core.Check, error) {
	checkGroup, err := core.NewCheckGroup(ctx, mod, []string{args.Name}, false)
	if err != nil {
		return nil, err
	}

	switch len(checkGroup.Checks) {
	case 1:
		return checkGroup.Checks[0].Clone(), nil
	case 0:
		return nil, fmt.Errorf("check %q not found in module %q", args.Name, mod.Self().Name())
	default:
		return nil, fmt.Errorf("multiple checks found with name %q in module %q", args.Name, mod.Self().Name())
	}
}

func (s *moduleSchema) moduleGenerators(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.GeneratorGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}
	return core.NewGeneratorGroup(ctx, mod, include)
}

func (s *moduleSchema) moduleServices(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.UpGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}
	return core.NewUpGroup(ctx, mod, include)
}

func (s *moduleSchema) currentModuleGenerators(
	ctx context.Context,
	mod *core.CurrentModule,
	args struct {
		Include dagql.Optional[dagql.ArrayInput[dagql.String]]
	},
) (*core.GeneratorGroup, error) {
	var include []string
	if args.Include.Valid {
		for _, pattern := range args.Include.Value {
			include = append(include, pattern.String())
		}
	}
	return core.NewGeneratorGroup(ctx, mod.Module, include)
}

func (s *moduleSchema) moduleGenerator(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	args struct {
		Name string
	},
) (*core.Generator, error) {
	generatorGroup, err := core.NewGeneratorGroup(ctx, mod, []string{args.Name})
	if err != nil {
		return nil, err
	}

	switch len(generatorGroup.Generators) {
	case 1:
		return generatorGroup.Generators[0].Clone(), nil
	case 0:
		return nil, fmt.Errorf("generator %q not found in module %q", args.Name, mod.Self().Name())
	default:
		return nil, fmt.Errorf("multiple generators found with name %q in module %q", args.Name, mod.Self().Name())
	}
}

func (s *moduleSchema) moduleDependencies(
	ctx context.Context,
	mod *core.Module,
	args struct{},
) (dagql.Array[*core.Module], error) {
	depMods := make([]*core.Module, 0, len(mod.Deps.Mods()))
	for _, dep := range mod.Deps.Mods() {
		if depInst := dep.ModuleResult(); depInst.Self() != nil {
			depMods = append(depMods, depInst.Self())
			continue
		}
		switch dep.(type) {
		case *CoreMod:
			// skip
		default:
			return nil, fmt.Errorf("unexpected mod dependency type %T", dep)
		}
	}
	return depMods, nil
}

func (s *moduleSchema) moduleWithDescription(ctx context.Context, mod *core.Module, args struct {
	Description string
}) (*core.Module, error) {
	return mod.WithDescription(args.Description), nil
}

func (s *moduleSchema) moduleWithObject(ctx context.Context, mod *core.Module, args struct {
	Object core.TypeDefID
}) (_ *core.Module, rerr error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	def, err := args.Object.Load(ctx, dag)
	if err != nil {
		return nil, err
	}
	return core.EnvHook{Server: dag}.ModuleWithObject(ctx, mod, def)
}

func (s *moduleSchema) moduleWithInterface(ctx context.Context, mod *core.Module, args struct {
	Iface core.TypeDefID
}) (_ *core.Module, rerr error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	def, err := args.Iface.Load(ctx, dag)
	if err != nil {
		return nil, err
	}
	return mod.WithInterface(ctx, def)
}

func (s *moduleSchema) moduleWithEnum(ctx context.Context, mod *core.Module, args struct {
	Enum core.TypeDefID
}) (_ *core.Module, rerr error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	def, err := args.Enum.Load(ctx, dag)
	if err != nil {
		return nil, err
	}

	return mod.WithEnum(ctx, def)
}

func (s *moduleSchema) currentModuleName(
	ctx context.Context,
	curMod *core.CurrentModule,
	args struct{},
) (string, error) {
	return curMod.Module.Self().NameField, nil
}

func (s *moduleSchema) currentModuleGeneratedContextDirectory(
	ctx context.Context,
	mod dagql.ObjectResult[*core.CurrentModule],
	args struct{},
) (inst dagql.Result[*core.Directory], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	err = dag.Select(ctx, mod.Self().Module.Self().Source.Value, &inst,
		dagql.Selector{
			Field: "generatedContextDirectory",
		},
	)
	return inst, err
}

func (s *moduleSchema) currentModuleDependencies(
	ctx context.Context,
	mod *core.CurrentModule,
	args struct{},
) (dagql.Array[*core.Module], error) {
	depMods := make([]*core.Module, 0, len(mod.Module.Self().Deps.Mods()))
	for _, dep := range mod.Module.Self().Deps.Mods() {
		if depInst := dep.ModuleResult(); depInst.Self() != nil {
			depMods = append(depMods, depInst.Self())
			continue
		}
		switch dep.(type) {
		case *CoreMod:
			// skip
		default:
			return nil, fmt.Errorf("unexpected mod dependency type %T", dep)
		}
	}
	return depMods, nil
}

func (s *moduleSchema) currentModuleSource(
	ctx context.Context,
	curMod dagql.ObjectResult[*core.CurrentModule],
	args struct{},
) (inst dagql.Result[*core.Directory], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	curSrc := curMod.Self().Module.Self().Source.Value
	if curSrc.Self() == nil {
		return inst, errors.New("invalid unset current module source")
	}

	srcSubpath := curSrc.Self().SourceSubpath
	if srcSubpath == "" {
		srcSubpath = curSrc.Self().SourceRootSubpath
	}

	var generatedDiff dagql.Result[*core.Directory]
	err = dag.Select(ctx, curSrc, &generatedDiff,
		dagql.Selector{Field: "generatedContextDirectory"},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to get generated context directory: %w", err)
	}
	generatedDiffID, err := generatedDiff.ID()
	if err != nil {
		return inst, fmt.Errorf("failed to get generated context directory ID: %w", err)
	}

	err = dag.Select(ctx, curSrc.Self().ContextDirectory, &inst,
		dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String("/")},
				{Name: "source", Value: dagql.NewID[*core.Directory](generatedDiffID)},
			},
		},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(srcSubpath)},
			},
		},
	)
	if err != nil {
		return inst, fmt.Errorf("failed to get source directory: %w", err)
	}

	return inst, err
}

func (s *moduleSchema) currentModuleWorkdir(
	ctx context.Context,
	curMod dagql.ObjectResult[*core.CurrentModule],
	args struct {
		Path string
		core.CopyFilter
	},
) (inst dagql.Result[*core.Directory], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	if !filepath.IsLocal(args.Path) {
		return inst, fmt.Errorf("workdir path %q escapes workdir", args.Path)
	}
	args.Path = filepath.Join(sdk.RuntimeWorkdirPath, args.Path)

	err = dag.Select(ctx, dag.Root(), &inst,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "directory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(args.Path)},
				{Name: "exclude", Value: asArrayInput(args.Exclude, dagql.NewString)},
				{Name: "include", Value: asArrayInput(args.Include, dagql.NewString)},
				{Name: "gitignore", Value: dagql.Boolean(args.Gitignore)},
			},
		},
	)
	return inst, err
}

func (s *moduleSchema) currentModuleWorkdirFile(
	ctx context.Context,
	curMod dagql.ObjectResult[*core.CurrentModule],
	args struct {
		Path string
	},
) (inst dagql.Result[*core.File], err error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}

	if !filepath.IsLocal(args.Path) {
		return inst, fmt.Errorf("workdir path %q escapes workdir", args.Path)
	}
	args.Path = filepath.Join(sdk.RuntimeWorkdirPath, args.Path)

	err = dag.Select(ctx, dag.Root(), &inst,
		dagql.Selector{
			Field: "host",
		},
		dagql.Selector{
			Field: "file",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(args.Path)},
			},
		},
	)
	return inst, err
}

func (s *moduleSchema) loadSourceMapResult(ctx context.Context, sourceMap dagql.Optional[core.SourceMapID]) (dagql.ObjectResult[*core.SourceMap], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.SourceMap]{}, fmt.Errorf("failed to get dag server: %w", err)
	}

	if !sourceMap.Valid {
		return dagql.ObjectResult[*core.SourceMap]{}, nil
	}
	sourceMapI, err := sourceMap.Value.Load(ctx, dag)
	if err != nil {
		return dagql.ObjectResult[*core.SourceMap]{}, fmt.Errorf("failed to decode source map: %w", err)
	}
	return sourceMapI, nil
}

func idInput[T dagql.Typed](res dagql.ObjectResult[T]) dagql.ID[T] {
	id, err := res.ID()
	if err != nil {
		panic(err)
	}
	return dagql.NewID[T](id)
}

func optID[T dagql.Typed](res dagql.ObjectResult[T]) dagql.Optional[dagql.ID[T]] {
	id, err := core.OptionalResultIDInput(res)
	if err != nil {
		panic(err)
	}
	return id
}

func optString(v *string) dagql.Optional[dagql.String] {
	if v == nil {
		return dagql.Optional[dagql.String]{}
	}
	return dagql.Opt(dagql.String(*v))
}

func (s *moduleSchema) moduleImplementationScoped(
	ctx context.Context,
	parentMod dagql.ObjectResult[*core.Module],
	args struct{},
) (inst dagql.ObjectResult[*core.Module], err error) {
	if !parentMod.Self().Source.Valid {
		return inst, fmt.Errorf("failed to get source implementation digest for module: no module source available")
	}
	sourceDigest, err := parentMod.Self().Source.Value.Self().SourceImplementationDigest(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get source implementation digest for module: %w", err)
	}
	scopedDigest := hashutil.HashStrings("Module._implementationScoped", sourceDigest.String())
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, fmt.Errorf("failed to get dag server: %w", err)
	}
	inst, err = dagql.NewObjectResultForCurrentCall(ctx, dag, parentMod.Self())
	if err != nil {
		return inst, err
	}
	return inst.WithContentDigest(ctx, scopedDigest)
}
