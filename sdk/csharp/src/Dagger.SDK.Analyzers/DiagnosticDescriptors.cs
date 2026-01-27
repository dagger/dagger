using Microsoft.CodeAnalysis;

namespace Dagger.SDK.Analyzers;

public static class DiagnosticDescriptors
{
    private const string Category = "Dagger";

    public static readonly DiagnosticDescriptor PublicMethodInObjectMissingFunctionAttribute = new(
        id: "DAGGER001",
        title: "Public method in Dagger Object should have [Function] attribute",
        messageFormat: "Public method '{0}' in class marked with [Object] should be marked with [Function] attribute to be exposed as a Dagger function",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Info,
        isEnabledByDefault: true,
        description: "Public methods in classes marked with [Object] should have the [Function] attribute to be exposed in the Dagger API."
    );

    public static readonly DiagnosticDescriptor FunctionMissingXmlDocumentation = new(
        id: "DAGGER002",
        title: "Dagger function should have XML documentation",
        messageFormat: "Function '{0}' should have XML documentation comments to provide description in 'dagger functions' output",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Info,
        isEnabledByDefault: true,
        description: "Functions marked with [Function] should have XML documentation comments (<summary>) to provide helpful descriptions."
    );

    public static readonly DiagnosticDescriptor ParameterMissingXmlDocumentation = new(
        id: "DAGGER003",
        title: "Dagger function parameter should have XML documentation",
        messageFormat: "Parameter '{0}' should have XML documentation (<param>) to provide description in Dagger API",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Info,
        isEnabledByDefault: true,
        description: "Parameters in functions marked with [Function] should have XML documentation to provide helpful parameter descriptions."
    );

    public static readonly DiagnosticDescriptor DirectoryParameterShouldHaveDefaultPath = new(
        id: "DAGGER004",
        title: "Directory parameter should consider [DefaultPath] attribute",
        messageFormat: "Parameter '{0}' of type Directory might benefit from [DefaultPath] attribute to specify the default source path",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Info,
        isEnabledByDefault: true,
        description: "Directory parameters can use [DefaultPath] to specify which directory to load by default (e.g., [DefaultPath(\".\")] for current directory)."
    );

    public static readonly DiagnosticDescriptor DirectoryParameterShouldHaveIgnore = new(
        id: "DAGGER005",
        title: "Directory parameter should consider [Ignore] attribute",
        messageFormat: "Parameter '{0}' of type Directory might benefit from [Ignore] attribute to exclude unwanted files (e.g., node_modules, .git)",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Info,
        isEnabledByDefault: true,
        description: "Directory parameters can use [Ignore] to specify glob patterns for files to exclude (e.g., [Ignore(\"node_modules\", \".git\")])."
    );

    public static readonly DiagnosticDescriptor ObjectClassMissingXmlDocumentation = new(
        id: "DAGGER006",
        title: "Dagger Object should have XML documentation",
        messageFormat: "Class '{0}' marked with [Object] should have XML documentation to provide module description",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Info,
        isEnabledByDefault: true,
        description: "Classes marked with [Object] should have XML documentation comments to provide helpful module descriptions."
    );

    public static readonly DiagnosticDescriptor FieldMissingXmlDocumentation = new(
        id: "DAGGER007",
        title: "Dagger field should have XML documentation",
        messageFormat: "Property '{0}' marked with [Function] should have XML documentation to provide field description",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Info,
        isEnabledByDefault: true,
        description: "Properties marked with [Function] should have XML documentation comments to provide helpful field descriptions."
    );

    public static readonly DiagnosticDescriptor InvalidFunctionCacheValue = new(
        id: "DAGGER008",
        title: "Invalid Cache value in Function attribute",
        messageFormat: "Cache value '{0}' is invalid. Use 'never', 'session', or a duration string like '5m', '1h', '30s'.",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Warning,
        isEnabledByDefault: true,
        description: "The Cache property in [Function] must be 'never', 'session', or a valid duration string (e.g., '10s', '5m', '2h')."
    );

    public static readonly DiagnosticDescriptor IgnoreAttributeOnInvalidParameterType = new(
        id: "DAGGER009",
        title: "[Ignore] attribute can only be applied to Directory parameters",
        messageFormat: "Parameter '{0}' has [Ignore] attribute but is of type '{1}'. [Ignore] can only be used on Directory parameters",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Error,
        isEnabledByDefault: true,
        description: "The [Ignore] attribute is only valid on parameters of type Directory. The Dagger engine validates this at runtime and will reject functions with [Ignore] on non-Directory parameters."
    );

    public static readonly DiagnosticDescriptor DefaultPathAttributeOnInvalidParameterType = new(
        id: "DAGGER010",
        title: "[DefaultPath] attribute can only be applied to Directory or File parameters",
        messageFormat: "Parameter '{0}' has [DefaultPath] attribute but is of type '{1}'. [DefaultPath] can only be used on Directory or File parameters",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Error,
        isEnabledByDefault: true,
        description: "The [DefaultPath] attribute is only valid on parameters of type Directory or File. The Dagger engine validates this at runtime and will reject functions with [DefaultPath] on other parameter types."
    );

    public static readonly DiagnosticDescriptor MissingDaggerJson = new(
        id: "DAGGER011",
        title: "Dagger module requires dagger.json configuration file",
        messageFormat: "Dagger module with [Object] class requires dagger.json configuration file (searched up to '{0}')",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Warning,
        isEnabledByDefault: true,
        description: "Dagger modules require a dagger.json configuration file in the module directory or a parent directory. Run 'dagger init' to create one."
    );

    public static readonly DiagnosticDescriptor ModuleClassNameMismatch = new(
        id: "DAGGER012",
        title: "Module must have a root class matching dagger.json module name",
        messageFormat: "One [Object] class must be named '{0}' to match dagger.json module name '{1}' and serve as the module root",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Warning,
        isEnabledByDefault: true,
        description: "Dagger modules require one class with [Object] attribute to be named according to the module name in dagger.json. This class serves as the module root. For example, 'my-module' requires a class named 'MyModule'.",
        customTags: WellKnownDiagnosticTags.CompilationEnd
    );

    public static readonly DiagnosticDescriptor ProjectFileNameMismatch = new(
        id: "DAGGER013",
        title: "Project file name should match dagger.json module name",
        messageFormat: "Project file should be named '{0}.csproj' to match dagger.json module name '{1}' (currently '{2}.csproj')",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Warning,
        isEnabledByDefault: true,
        description: "The .csproj file name should match the PascalCase transformation of the module name in dagger.json. Renaming requires closing and reopening the solution.",
        customTags: WellKnownDiagnosticTags.CompilationEnd
    );

    public static readonly DiagnosticDescriptor CustomReturnTypeMissingObjectAttribute = new(
        id: "DAGGER014",
        title: "Custom return type must have [Object] attribute",
        messageFormat: "Return type '{0}' must be decorated with [Object] attribute to be used as a Dagger function return type",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Error,
        isEnabledByDefault: true,
        description: "Custom types returned from [Function] methods must be decorated with [Object] and have their properties marked with [Function]. This is required for Dagger's reflection-based serialization."
    );

    public static readonly DiagnosticDescriptor EnumMemberMissingEnumValueAttribute = new(
        id: "DAGGER015",
        title: "Enum member should have [EnumValue] attribute",
        messageFormat: "Enum member '{0}' in enum '{1}' marked with [Enum] should have [EnumValue] attribute for metadata support",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Info,
        isEnabledByDefault: true,
        description: "Enum members in enums marked with [Enum] should have the [EnumValue] attribute to provide descriptions and deprecation information."
    );

    public static readonly DiagnosticDescriptor CheckFunctionWithRequiredParameters = new(
        id: "DAGGER016",
        title: "Check function must not have required parameters",
        messageFormat: "Function '{0}' marked with [Check] has required parameters ({1}). Check functions must only have optional parameters or parameters with [DefaultPath]",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Error,
        isEnabledByDefault: true,
        description: "Functions marked with [Check] are validation functions that the Dagger engine scans automatically. They must not require arguments - all parameters must be optional (nullable, have default values, or use [DefaultPath])."
    );

    public static readonly DiagnosticDescriptor CheckFunctionInvalidReturnType = new(
        id: "DAGGER017",
        title: "Check function has invalid return type",
        messageFormat: "Function '{0}' marked with [Check] returns '{1}'. Check functions must return void, Task, Container, or Task<Container>",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Error,
        isEnabledByDefault: true,
        description: "Functions marked with [Check] must return void (throws on failure), Task (async throws on failure), Container (exit code validation), or Task<Container> (async container validation). Other return types are not supported."
    );

    public static readonly DiagnosticDescriptor MultiplePublicConstructors = new(
        id: "DAGGER018",
        title: "Dagger module class should have only one public constructor",
        messageFormat: "Class '{0}' marked with [Object] has {1} public constructors. Dagger modules support only one constructor - the first constructor with parameters will be used, others will be ignored",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Warning,
        isEnabledByDefault: true,
        description: "Dagger modules have only one constructor. If multiple public constructors are defined, only the first constructor with parameters will be registered. Define a single public constructor or use private constructors with static factory methods."
    );

    public static readonly DiagnosticDescriptor ConstructorAttributeOnNonStaticMethod = new(
        id: "DAGGER019",
        title: "Constructor attribute can only be applied to static methods",
        messageFormat: "Method '{0}' marked with [Constructor] is not static. [Constructor] can only be applied to static factory methods",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Error,
        isEnabledByDefault: true,
        description: "[Constructor] attribute marks static factory methods as alternative constructors. These must be static methods that return an instance of the containing class (or Task/ValueTask of the class)."
    );

    public static readonly DiagnosticDescriptor ConstructorAttributeInvalidReturnType = new(
        id: "DAGGER020",
        title: "Constructor method must return the containing class type",
        messageFormat: "Method '{0}' marked with [Constructor] returns '{1}' but must return '{2}' (or Task<{2}>/ValueTask<{2}>)",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Error,
        isEnabledByDefault: true,
        description: "[Constructor] methods must return an instance of the containing class type, or Task/ValueTask wrapping that type for async initialization."
    );

    public static readonly DiagnosticDescriptor MultipleConstructorAttributes = new(
        id: "DAGGER021",
        title: "Dagger module class should have only one method with [Constructor] attribute",
        messageFormat: "Class '{0}' has {1} methods marked with [Constructor]. Only one [Constructor] method is allowed per class",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Error,
        isEnabledByDefault: true,
        description: "Only one [Constructor] attribute is allowed per class. If you need multiple factory methods, use different names and make only one the constructor."
    );

    public static readonly DiagnosticDescriptor FieldPropertyMustHaveSetter = new(
        id: "DAGGER022",
        title: "Field property must have a setter for serialization",
        messageFormat: "Property '{0}' marked with [Function] must have a setter (can be private) for Dagger serialization to work correctly",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Error,
        isEnabledByDefault: true,
        description: "Properties marked with [Function] require a setter (can be 'private set') for Dagger's serialization/deserialization to work when objects are passed between function calls. Without a setter, you'll get 'Property set method not found' runtime errors."
    );

    public static readonly DiagnosticDescriptor ConstructorParameterShouldMapToPublicProperty = new(
        id: "DAGGER023",
        title: "Constructor parameter should map to a public property",
        messageFormat: "Constructor parameter '{0}' does not map to a public property. Dagger objects must use public properties (not private fields) for state that persists across function calls",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Warning,
        isEnabledByDefault: true,
        description: "Dagger serializes objects as JSON using public properties. Constructor parameters should set public properties with matching names (case-insensitive). Private fields set by the constructor won't be preserved when the object is reconstructed for subsequent function calls."
    );
}
