using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Dagger.SDK.SourceGenerator.Extensions;
using Dagger.SDK.SourceGenerator.Types;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Type = Dagger.SDK.SourceGenerator.Types.Type;

namespace Dagger.SDK.SourceGenerator.Code;

public class CodeRenderer : ICodeRenderer
{
    /// <summary>
    /// Set of all known OBJECT type names in the schema (for resolving @expectedType).
    /// Must be set before calling Render methods.
    /// </summary>
    public HashSet<string> ObjectTypeNames { get; set; } = new();

    /// <summary>
    /// Set of all known INTERFACE type names in the schema.
    /// </summary>
    public HashSet<string> InterfaceTypeNames { get; set; } = new();

    public string RenderPre()
    {
        return """
            #nullable enable

            using System.Collections.Immutable;
            using System.Text.Json.Serialization;

            using Dagger.SDK.GraphQL;
            using Dagger.SDK.JsonConverters;

            namespace Dagger.SDK;
            """;
    }

    public string RenderEnum(Type type)
    {
        var evs = type.EnumValues.Select(ev => ev.Name);
        return $$"""
            {{RenderDocComment(type)}}
            [JsonConverter(typeof(JsonStringEnumConverter<{{type.Name}}>))]
            public enum {{Formatter.FormatType(type.Name)}}
            {
                {{string.Join(",", evs)}}
            }
            """;
    }

    public string RenderInputObject(Type type)
    {
        var properties = type.InputFields.Select(field =>
            $$"""
            {{RenderDocComment(field)}}
            public {{GetArgTypeName(field)}} {{Formatter.FormatProperty(
                field.Name
            )}} { get; } = {{field.GetVarName()}};
            """
        );

        var constructorFields = type.InputFields.Select(field =>
            $"{GetArgTypeName(field)} {field.GetVarName()}"
        );

        var toKeyValuePairsProperties = type.InputFields.Select(field =>
            $"""
            kvPairs.Add(new KeyValuePair<string, Value>("{field.Name}", {RenderArgumentValue(
                field,
                asProperty: true
            )} as Value));
            """
        );

        var toKeyValuePairsMethod = $$"""
            public List<KeyValuePair<string, Value>> ToKeyValuePairs()
            {
                var kvPairs = new List<KeyValuePair<string, Value>>();
                {{string.Join("\n", toKeyValuePairsProperties)}}
                return kvPairs;
            }
            """;

        return $$"""
            {{RenderDocComment(type)}}
            public struct {{Formatter.FormatType(type.Name)}}({{string.Join(
                ", ",
                constructorFields
            )}}) : IInputObject
            {
                {{string.Join("\n\n", properties)}}
                {{toKeyValuePairsMethod}}
            }
            """;
    }

    public string RenderObject(Type type)
    {
        return RenderObjectOrInterfaceClient(type, isInterface: false);
    }

    public string RenderInterface(Type type)
    {
        var interfaceName = Formatter.FormatType(type.Name);

        // Generate the C# interface
        // Skip the "id" field since IId already declares IdAsync
        var interfaceMethods = type.Fields
            .Where(field => field.Name != "id")
            .Select(field =>
        {
            var isAsync = IsAsyncField(field, type.Name);
            var methodName = Formatter.FormatMethod(field.Name);

            if (type.Name.Equals(field.Name, StringComparison.CurrentCultureIgnoreCase))
            {
                methodName = $"{methodName}_";
            }

            if (isAsync)
            {
                methodName = $"{methodName}Async";
            }

            var requiredArgs = field.RequiredArgs();
            var optionalArgs = field.OptionalArgs();
            var args = requiredArgs
                .Select(RenderArgument)
                .Concat(optionalArgs.Select(RenderOptionalArgument))
                .Concat(isAsync ? new[] { "CancellationToken cancellationToken = default" } : []);

            // Interface methods can't use 'async' — just declare the Task<T> return type
            var returnType = RenderReturnType(field, type.Name).Replace("async ", "");

            return $$"""
            {{RenderDocComment(field)}}
            {{RenderObsolete(field)}}
            {{returnType}} {{methodName}}({{string.Join(",", args)}});
            """;
        });

        var interfaceCode = $$"""
            {{RenderDocComment(type)}}
            public interface {{interfaceName}} : IId
            {
                {{string.Join("\n\n", interfaceMethods)}}
            }
            """;

        // Generate the client class that implements the interface
        var clientCode = RenderObjectOrInterfaceClient(type, isInterface: true);

        return $"{interfaceCode}\n\n{clientCode}";
    }

    private string RenderObjectOrInterfaceClient(Type type, bool isInterface)
    {
        var className = isInterface
            ? $"{Formatter.FormatType(type.Name)}Client"
            : Formatter.FormatType(type.Name);

        var methods = type.Fields.Select(field =>
        {
            var isAsync = IsAsyncField(field, type.Name);
            var methodName = Formatter.FormatMethod(field.Name);

            if (type.Name.Equals(field.Name, StringComparison.CurrentCultureIgnoreCase))
            {
                methodName = $"{methodName}_";
            }

            if (isAsync)
            {
                methodName = $"{methodName}Async";
            }

            var requiredArgs = field.RequiredArgs();
            var optionalArgs = field.OptionalArgs();
            var args = requiredArgs
                .Select(RenderArgument)
                .Concat(optionalArgs.Select(RenderOptionalArgument))
                .Concat(isAsync ? new[] { "CancellationToken cancellationToken = default" } : []);

            return $$"""
            {{RenderDocComment(field)}}
            {{RenderObsolete(field)}}
            public {{RenderReturnType(field, type.Name)}} {{methodName}}({{string.Join(",", args)}})
            {
                {{RenderArgumentBuilder(field)}}
                {{RenderQueryBuilder(field)}}
                return {{RenderReturnValue(field, type.Name)}};
            }
            """;
        });

        // Build the implements clause
        var implementsList = new List<string>();

        // Add IId for objects with an id field
        if (type.Fields.Any(field => field.Name == "id"))
        {
            implementsList.Add("IId");
        }

        // For interface client classes, implement the interface
        if (isInterface)
        {
            implementsList.Add(Formatter.FormatType(type.Name));
        }
        else
        {
            // For objects, implement any interfaces they declare
            foreach (var iface in type.Interfaces)
            {
                var ifaceName = iface.GetType_().Name;
                if (!string.IsNullOrEmpty(ifaceName) && ifaceName != "Node")
                {
                    implementsList.Add(Formatter.FormatType(ifaceName));
                }
            }
        }

        var baseClass = "Object(queryBuilder, gqlClient)";
        var implementsClause = implementsList.Count > 0
            ? $", {string.Join(", ", implementsList)}"
            : "";

        return $$"""
            {{RenderDocComment(type)}}
            public class {{className}}(QueryBuilder queryBuilder, GraphQLClient gqlClient) : {{baseClass}}{{implementsClause}}
            {
                {{string.Join("\n\n", methods)}}
            }
            """;
    }

    public string RenderScalar(Type type)
    {
        var t = Formatter.FormatType(type.Name);
        return $$"""
            {{RenderDocComment(type)}}
            [JsonConverter(typeof(ScalarIdConverter<{{t}}>))]
            public class {{Formatter.FormatType(t)}} : Scalar
            {
            }
            """;
    }

    public string Format(string source)
    {
        return CSharpSyntaxTree
            .ParseText(source)
            .GetRoot()
            .NormalizeWhitespace(eol: "\n")
            .ToFullString();
    }

    // ─── Helpers ──────────────────────────────────────────────

    /// <summary>
    /// Determine if a field should be async (returns a leaf/list value, or is sync-like).
    /// </summary>
    private bool IsAsyncField(Field field, string parentTypeName)
    {
        // sync-like field: @expectedType matches parent → async (executes to get ID, returns parent)
        if (IsSyncLikeField(field, parentTypeName))
        {
            return true;
        }

        return field.Type.IsLeaf() || field.Type.IsList();
    }

    /// <summary>
    /// Check if a field is sync-like (@expectedType matches parent type name).
    /// </summary>
    private static bool IsSyncLikeField(Field field, string parentTypeName)
    {
        var expectedType = field.GetExpectedType();
        return expectedType != null && expectedType == parentTypeName;
    }

    /// <summary>
    /// Get the type name for an argument, resolving @expectedType.
    /// </summary>
    private string GetArgTypeName(InputValue arg)
    {
        var expectedType = arg.GetExpectedType();
        if (expectedType != null)
        {
            var tr = arg.Type.GetType_();
            if (tr.IsList())
            {
                return $"{Formatter.FormatType(expectedType)}[]";
            }
            return Formatter.FormatType(expectedType);
        }

        return GetNormalizedTypeName(arg);
    }

    /// <summary>
    /// Get the type name for an argument list element, resolving @expectedType.
    /// </summary>
    private string GetArgElementTypeName(InputValue arg)
    {
        var expectedType = arg.GetExpectedType();
        if (expectedType != null)
        {
            return Formatter.FormatType(expectedType);
        }

        var tr = arg.Type.GetType_();
        if (tr.IsList())
        {
            return tr.OfType!.GetTypeName();
        }
        return tr.GetTypeName();
    }

    /// <summary>
    /// Check if a type name refers to an object or interface type (i.e., an IId type).
    /// </summary>
    private bool IsIdableType(string typeName)
    {
        return ObjectTypeNames.Contains(typeName) || InterfaceTypeNames.Contains(typeName);
    }

    private static string RenderObsolete(Field field)
    {
        if (!field.IsDeprecated)
            return "";
        string escapedReason = SymbolDisplay.FormatLiteral(field.DeprecationReason, true);
        return $"[Obsolete({escapedReason})]";
    }

    private static string RenderDocComment(Type type)
    {
        return RenderSummaryDocComment(type.Description);
    }

    private static string RenderDocComment(Field field)
    {
        var builder = new StringBuilder();
        builder.AppendLine(RenderSummaryDocComment(field.Description));
        builder = field.Args.Aggregate(
            builder,
            (sb, arg) =>
            {
                string[] lines = arg.Description.Split('\n');
                sb.AppendLine($"/// <param name=\"{arg.GetVarName()}\">");
                foreach (var line in lines)
                {
                    sb.AppendLine($"/// {line}");
                }

                sb.AppendLine($"/// </param>");
                return sb;
            }
        );
        return builder.ToString();
    }

    private static string RenderDocComment(InputValue field)
    {
        return RenderSummaryDocComment(field.Description);
    }

    private static string RenderSummaryDocComment(string doc)
    {
        if (string.IsNullOrEmpty(doc))
        {
            return "";
        }

        var description = doc.Split('\n').Select(line => $"/// {line}").Select(line => line.Trim());
        return $"""
            /// <summary>
            {string.Join("\n", description)}
            /// </summary>
            """;
    }

    private static string GetNormalizedTypeName(InputValue arg)
    {
        return GetNormalizedTypeName(arg.Type, arg.Name);
    }

    private static string GetNormalizedTypeName(TypeRef typeRef, string name)
    {
        var tr = typeRef.GetType_();
        if (tr.IsList())
        {
            return $"{GetNormalizedTypeName(tr.OfType!, name)}[]";
        }

        var type = tr.GetTypeName();
        return type;
    }

    private string RenderArgument(InputValue arg)
    {
        return $"{GetArgTypeName(arg)} {arg.GetVarName()}";
    }

    private string RenderOptionalArgument(InputValue arg)
    {
        var nullableType = GetArgTypeName(arg) + "?";

        if (arg.DefaultValue != null)
        {
            if (arg.Type.IsList() && arg.DefaultValue == "[]")
            {
                return $"{nullableType} {arg.GetVarName()} = null";
            }

            if (arg.Type.IsScalar() && string.IsNullOrWhiteSpace(arg.DefaultValue.Trim('"')))
            {
                return $"{nullableType} {arg.GetVarName()} = null";
            }

            if (arg.Type.IsEnum() && !string.IsNullOrWhiteSpace(arg.DefaultValue.Trim('"')))
            {
                return $"{nullableType} {arg.GetVarName()} = {GetArgTypeName(arg)}.{arg.DefaultValue}";
            }

            return $"{nullableType} {arg.GetVarName()} = {arg.DefaultValue}";
        }

        return $"{nullableType} {arg.GetVarName()} = null";
    }

    private string RenderReturnType(Field field, string parentTypeName)
    {
        var type = field.Type;

        // sync-like: @expectedType matches parent → async, returns parent type
        if (IsSyncLikeField(field, parentTypeName))
        {
            var formatted = Formatter.FormatType(parentTypeName);
            var className = InterfaceTypeNames.Contains(parentTypeName)
                ? $"{formatted}Client" : formatted;
            return $"async Task<{className}>";
        }

        if (type.IsLeaf() || type.IsList())
        {
            return $"async Task<{type.GetTypeName()}>";
        }

        return type.GetTypeName();
    }

    private string RenderReturnValue(Field field, string parentTypeName)
    {
        var type = field.Type;

        // sync-like: @expectedType matches parent → execute query to get ID, return new object via node(id:)
        if (IsSyncLikeField(field, parentTypeName))
        {
            var formatted = Formatter.FormatType(parentTypeName);
            var className = InterfaceTypeNames.Contains(parentTypeName)
                ? $"{formatted}Client" : formatted;
            return $"new {className}(Object.NodeQueryBuilder((await QueryExecutor.ExecuteAsync<Id>(GraphQLClient, queryBuilder, cancellationToken)).Value, \"{parentTypeName}\"), GraphQLClient)";
        }

        if (type.IsLeaf())
        {
            return $"await QueryExecutor.ExecuteAsync<{field.Type.GetTypeName()}>(GraphQLClient, queryBuilder, cancellationToken)";
        }

        if (type.IsList() && type.GetType_().OfType!.IsObjectOrInterface())
        {
            var typeName = type.GetType_().OfType!.GetType_().Name;
            var formattedName = Formatter.FormatType(typeName);
            // Use the client class for interface types
            var clientClassName = InterfaceTypeNames.Contains(typeName)
                ? $"{formattedName}Client"
                : formattedName;
            return $"""
                (await QueryExecutor.ExecuteListAsync<Id>(GraphQLClient, queryBuilder, cancellationToken))
                    .Select(id =>
                        new {clientClassName}(
                            Object.NodeQueryBuilder(id.Value, "{typeName}"),
                            GraphQLClient
                        )
                    )
                    .ToArray()
                """;
        }

        if (type.IsList() && type.GetType_().OfType!.IsScalar())
        {
            var typeName = type.GetType_().OfType!.GetTypeName();
            return $"await QueryExecutor.ExecuteListAsync<{typeName}>(GraphQLClient, queryBuilder, cancellationToken)";
        }

        // For interface return types, use the client class
        if (type.IsInterface())
        {
            var typeName = type.GetType_().Name;
            var clientClassName = $"{Formatter.FormatType(typeName)}Client";
            return $"new {clientClassName}(queryBuilder, GraphQLClient)";
        }

        return $"new {field.Type.GetTypeName()}(queryBuilder, GraphQLClient)";
    }

    private string RenderArgumentBuilder(Field field)
    {
        if (field.Args.Length == 0)
        {
            return "";
        }

        var builder = new StringBuilder("var arguments = ImmutableList<Argument>.Empty;");
        builder.Append('\n');

        var requiredArgs = field.RequiredArgs();
        if (requiredArgs.Any())
        {
            builder
                .Append("arguments = arguments.")
                .Append(
                    string.Join(
                        ".",
                        requiredArgs.Select(arg =>
                            $$"""Add(new Argument("{{arg.Name}}", {{RenderArgumentValue(arg)}}))"""
                        )
                    )
                )
                .Append(';');
            builder.Append('\n');
        }

        var optionalArgs = field.OptionalArgs();
        if (optionalArgs.Any())
        {
            optionalArgs
                .Aggregate(
                    builder,
                    (sb, arg) =>
                    {
                        var varName = arg.GetVarName();
                        return sb.Append(
                                $"""if ({varName} is {GetArgTypeName(arg)} {varName}_)"""
                            )
                            .Append("{\n")
                            .Append(
                                $$"""    arguments = arguments.Add(new Argument("{{arg.Name}}", {{RenderArgumentValue(arg, addVarSuffix: true)}}));"""
                            )
                            .Append("}\n");
                    }
                )
                .Append("\n");
        }

        return builder.ToString();
    }

    private string RenderArgumentValue(
        InputValue arg,
        bool addVarSuffix = false,
        bool asProperty = false
    )
    {
        var argName = arg.GetVarName();
        if (addVarSuffix)
        {
            argName = $"{argName}_";
        }

        if (asProperty)
        {
            argName = Formatter.FormatProperty(argName);
        }

        // If @expectedType resolves to an IId-able type, use IdValue
        var expectedType = arg.GetExpectedType();
        if (expectedType != null && IsIdableType(expectedType))
        {
            // List of IId-able types
            if (arg.Type.IsList())
            {
                return $"new ListValue({argName}.Select(v => new IdValue(v) as Value).ToList())";
            }
            return $"new IdValue({argName})";
        }

        if (arg.Type.IsScalar())
        {
            var type = arg.Type.GetTypeName();
            var token = SyntaxFacts.GetKeywordKind(type);

            switch (token)
            {
                case SyntaxKind.StringKeyword:
                    return $"new StringValue({argName})";
                case SyntaxKind.BoolKeyword:
                    return $"new BooleanValue({argName})";
                case SyntaxKind.IntKeyword:
                    return $"new IntValue({argName})";
                case SyntaxKind.FloatKeyword:
                    return $"new FloatValue({argName})";
                default:
                    // Unified ID scalar - just pass value
                    return $"new StringValue({argName}.Value)";
            }
        }

        if (arg.Type.IsEnum())
        {
            return $"new StringValue({argName}.ToString())";
        }

        if (arg.Type.IsInputObject())
        {
            return $"new ObjectValue({argName}.ToKeyValuePairs())";
        }

        if (arg.Type.IsList())
        {
            var tr = arg.Type.GetType_().OfType!.GetType_();

            if (tr.IsScalar())
            {
                var value = tr.GetTypeName() switch
                {
                    "string" => "new StringValue(v)",
                    "int" => "new IntValue(v)",
                    "float" => "new FloatValue(v)",
                    "boolean" => "new BooleanValue(v)",
                    _ => "new StringValue(v.Value)",
                };

                return $"new ListValue({argName}.Select(v => {value} as Value).ToList())";
            }

            if (tr.IsInputObject())
            {
                return $"new ListValue({argName}.Select(v => new ObjectValue(v.ToKeyValuePairs()) as Value).ToList())";
            }

            if (tr.IsEnum())
            {
                return $"new ListValue({argName}.Select(v => new StringValue(v.ToString()) as Value).ToList())";
            }

            throw new Exception(
                $"The type {tr.Describe_()} is not implemented as list value. This is a bug in the generator."
            );
        }

        throw new Exception($"The type {arg.Type.Describe_()} should not be enter here.");
    }

    private static string RenderQueryBuilder(Field field)
    {
        var builder = new StringBuilder("var queryBuilder = QueryBuilder.Select(");
        builder.Append($"\"{field.Name}\"");
        if (field.Args.Length > 0)
        {
            builder.Append(", arguments");
        }

        builder.Append(')');
        if (field.Type.IsList() && !field.Type.GetType_().OfType!.IsLeaf())
        {
            builder.Append(".Select(\"id\")");
        }

        builder.Append(';');
        return builder.ToString();
    }
}
