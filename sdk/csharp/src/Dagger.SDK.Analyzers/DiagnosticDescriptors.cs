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
        messageFormat: "Property '{0}' marked with [Field] should have XML documentation to provide field description",
        category: Category,
        defaultSeverity: DiagnosticSeverity.Info,
        isEnabledByDefault: true,
        description: "Properties marked with [Field] should have XML documentation comments to provide helpful field descriptions."
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
}
