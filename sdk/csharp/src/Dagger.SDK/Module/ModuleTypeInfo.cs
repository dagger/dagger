using System.Reflection;

namespace Dagger.ModuleRuntime;

/// <summary>
/// Represents information about a discovered module type.
/// </summary>
internal sealed class ModuleTypeInfo
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public string? Deprecated { get; init; }
    public required Type ClrType { get; init; }
    public ConstructorInfo? Constructor { get; set; }
    public List<FunctionInfo> Functions { get; } = new();
    public List<DaggerFieldInfo> Fields { get; } = new();
}

/// <summary>
/// Represents information about a discovered function.
/// </summary>
internal sealed class FunctionInfo
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public string? Deprecated { get; init; }
    public string? CachePolicy { get; init; }
    public required MethodInfo Method { get; init; }
    public required Type ReturnType { get; init; }
    public bool ReturnsTask { get; init; }
    public bool ReturnsValueTask { get; init; }
    public bool ReturnsVoid { get; init; }
    public List<ParameterMetadata> Parameters { get; } = new();
}

/// <summary>
/// Represents information about a function parameter.
/// </summary>
internal sealed class ParameterMetadata
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public required ParameterInfo Parameter { get; init; }
    public required Type ParameterType { get; init; }
    public bool IsOptional { get; init; }
    public bool IsCancellationToken { get; init; }
    public string? DefaultPath { get; init; }
    public List<string>? Ignore { get; init; }
    /// <summary>
    /// For interface types, stores the fully-namespaced GraphQL typename (e.g., "InterfaceExampleProcessor").
    /// For other types, this is null.
    /// </summary>
    public string? GraphQLTypeName { get; init; }
}

/// <summary>
/// Represents information about an object field (property).
/// </summary>
internal sealed class DaggerFieldInfo
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public string? Deprecated { get; init; }
    public required PropertyInfo PropertyInfo { get; init; }
    public required Type PropertyType { get; init; }
}

/// <summary>
/// Represents information about a discovered interface type.
/// </summary>
internal sealed class InterfaceTypeInfo
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public required Type ClrType { get; init; }
    public List<FunctionInfo> Functions { get; } = new();
}

/// <summary>
/// Represents information about an enum type.
/// </summary>
internal sealed class EnumTypeInfo
{
    public required string Name { get; init; }
    public string? Description { get; init; }
    public required Type EnumType { get; init; }
    public List<EnumValueInfo> Values { get; } = new();
}

/// <summary>
/// Represents information about an enum value.
/// </summary>
internal sealed class EnumValueInfo
{
    public required string Name { get; init; }
    public required string Value { get; init; }
    public string? Description { get; init; }
    public string? Deprecated { get; init; }
}
