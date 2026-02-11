using System;
using System.Collections.Generic;
using System.Linq;
using System.Text;
using Dagger.SDK.CodeGen.Extensions;
using Dagger.SDK.CodeGen.Types;
using Microsoft.CodeAnalysis;
using Microsoft.CodeAnalysis.CSharp;
using Type = Dagger.SDK.CodeGen.Types.Type;

namespace Dagger.SDK.CodeGen.Code;

public class CodeRenderer : ICodeRenderer
{
    public string RenderPre()
    {
        return """
            #nullable enable

            using System.Collections.Immutable;
            using System.Text.Json.Serialization;
            using System.Threading;
            using System.Threading.Tasks;

            using Dagger.GraphQL;
            using Dagger.JsonConverters;

            namespace Dagger;
            """;
    }

    public string RenderEnum(Type type)
    {
        var enumValuesWithDocs = type.EnumValues.Select(ev =>
        {
            var doc = string.IsNullOrEmpty(ev.Description)
                ? $"/// <summary>{ev.Name}</summary>"
                : RenderSummaryDocComment(ev.Description);
            return $"{doc}\n{ev.Name}";
        });

        return $$"""
            {{RenderDocComment(type)}}
            {{RenderExperimental(type)}}
            [JsonConverter(typeof(JsonStringEnumConverter<{{type.Name}}>))]
            public enum {{Formatter.FormatType(type.Name)}}
            {
                {{string.Join(",\n", enumValuesWithDocs)}}
            }
            """;
    }

    public string RenderInputObject(Type type)
    {
        var properties = type.InputFields.Select(field =>
            $$"""
            {{RenderDocComment(field)}}
            public {{field.Type.GetTypeName()}} {{Formatter.FormatProperty(
                field.Name
            )}} { get; } = {{field.GetVarName()}};
            """
        );

        var constructorFields = type.InputFields.Select(field =>
            $"{field.Type.GetTypeName()} {field.GetVarName()}"
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
            /// <summary>
            /// Converts this input object to GraphQL key-value pairs.
            /// </summary>
            public List<KeyValuePair<string, Value>> ToKeyValuePairs()
            {
                var kvPairs = new List<KeyValuePair<string, Value>>();
                {{string.Join("\n", toKeyValuePairsProperties)}}
                return kvPairs;
            }
            """;

        return $$"""
            {{RenderDocComment(type)}}
            {{RenderExperimental(type)}}
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
        var methods = type.Fields.Select(field =>
        {
            // Interface types are chainable (non-async) like object types
            var isAsync = (field.Type.IsLeaf() || field.Type.IsList()) && !field.Type.IsInterface();
            var methodName = Formatter.FormatMethod(field.Name);

            if (type.Name.Equals(field.Name, StringComparison.CurrentCultureIgnoreCase))
            {
                methodName = $"{methodName}_";
            }

            var requiredArgs = field.RequiredArgs();
            var optionalArgs = field.OptionalArgs();
            var args = requiredArgs
                .Select(RenderArgument)
                .Concat(optionalArgs.Select(RenderOptionalArgument))
                .Concat(isAsync ? new[] { "CancellationToken cancellationToken = default" } : []);

            return $$"""
            {{RenderDocComment(field)}}
            {{RenderExperimental(field, type.Name)}}
            {{RenderObsolete(field)}}
            public {{RenderReturnType(field.Type)}} {{methodName}}({{string.Join(",", args)}})
            {
                {{RenderArgumentBuilder(field)}}
                {{RenderQueryBuilder(field)}}
                return {{RenderReturnValue(field)}};
            }
            """;
        });

        // Generate interface conversion methods for each interface this object implements
        var interfaceConversionMethods =
            type.Interfaces?.Select(iface =>
            {
                var interfaceName = Formatter.FormatType(iface.Name);
                var wrapperName = $"{interfaceName}Wrapper";
                var methodName = $"As{interfaceName}";
                // GraphQL field name is camelCase version of the method name
                var graphqlFieldName =
                    char.ToLowerInvariant(methodName[0]) + methodName.Substring(1);

                return $$"""
                /// <summary>
                /// Convert this object to the {{interfaceName}} interface.
                /// </summary>
                public {{interfaceName}} {{methodName}}()
                {
                    var q = QueryBuilder.Select("{{graphqlFieldName}}");
                    // Interface conversion uses the wrapper class
                    // The wrapper needs to get the ID from the query result
                    return new {{wrapperName}}(GqlClient.Query, Id().Sync());
                }
                """;
            })
            ?? Enumerable.Empty<string>();

        var implementsIdInterface = "";
        if (type.Fields.Any(field => field.Name == "id"))
        {
            var idField = type.Fields.First(field => field.Name == "id");
            implementsIdInterface = $", IId<{idField.Type.GetTypeName()}>";
        }

        var allMethods = methods.Concat(interfaceConversionMethods);

        var className = Formatter.FormatType(type.Name);

        return $$"""
            {{RenderDocComment(type)}}
            {{RenderExperimental(type)}}
            public class {{className}}(QueryBuilder queryBuilder, GraphQLClient gqlClient) : Object(queryBuilder, gqlClient){{implementsIdInterface}}
            {
                {{string.Join("\n\n", allMethods)}}
            }
            """;
    }

    public string RenderScalar(Type type)
    {
        var t = Formatter.FormatType(type.Name);
        return $$"""
            {{RenderDocComment(type)}}
            {{RenderExperimental(type)}}
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

    private static string RenderObsolete(Field field)
    {
        if (!field.IsDeprecated)
            return string.Empty;
        string escapedReason = SymbolDisplay.FormatLiteral(field.DeprecationReason, true);
        return $"[Obsolete({escapedReason})]";
    }

    /// <summary>
    /// Generates a unique diagnostic ID for an experimental API.
    /// Format: DAGGER_<TYPE>_<METHOD>
    /// Examples: DAGGER_DIRECTORY_WITHPATCH, DAGGER_MODULE_CHECKS
    /// </summary>
    private static string GenerateDiagnosticId(string typeName, string memberName)
    {
        return $"DAGGER_{typeName.ToUpperInvariant()}_{memberName.ToUpperInvariant()}";
    }

    private static string RenderExperimental(Field field, string typeName)
    {
        if (!field.Directives.IsExperimental())
            return string.Empty;

        var reason = field.Directives.GetExperimentalReason();
        var escapedReason = reason != null ? SymbolDisplay.FormatLiteral(reason, true) : null;

        // Generate unique diagnostic ID per API for granular warning control
        // Follows Microsoft best practices: <PREFIX><IDENTIFIER> format
        // Example: Directory.WithPatch -> DAGGER_DIR_WITHPATCH
        var diagnosticId = GenerateDiagnosticId(typeName, field.Name);

        if (escapedReason != null)
        {
            return $"[System.Diagnostics.CodeAnalysis.Experimental(\"{diagnosticId}\")]";
        }
        else
        {
            return $"[System.Diagnostics.CodeAnalysis.Experimental(\"{diagnosticId}\")]";
        }
    }

    private static string RenderExperimental(Type type)
    {
        if (!type.Directives.IsExperimental())
            return string.Empty;

        var reason = type.Directives.GetExperimentalReason();
        var escapedReason = reason != null ? SymbolDisplay.FormatLiteral(reason, true) : null;

        // For types (enums, objects, etc.), use just the type name
        // Example: ExperimentalModule -> DAGGER_EXPERIMENTALMODULE
        var diagnosticId = $"DAGGER_{type.Name.ToUpperInvariant()}";

        if (escapedReason != null)
        {
            return $"[System.Diagnostics.CodeAnalysis.Experimental(\"{diagnosticId}\")]";
        }
        else
        {
            return $"[System.Diagnostics.CodeAnalysis.Experimental(\"{diagnosticId}\")]";
        }
    }

    private static string RenderDocComment(Type type)
    {
        return RenderSummaryDocComment(type.Description, type.Name);
    }

    private static string RenderDocComment(Field field)
    {
        var builder = new StringBuilder();
        builder.AppendLine(RenderSummaryDocComment(field.Description, field.Name));
        builder = field.Args.Aggregate(
            builder,
            (sb, arg) =>
            {
                string[] lines = arg.Description.Split('\n');
                sb.AppendLine(
                    $"/// <param name=\"{System.Security.SecurityElement.Escape(arg.GetVarName())}\">"
                );
                foreach (var line in lines)
                {
                    sb.AppendLine($"/// {System.Security.SecurityElement.Escape(line)}");
                }

                sb.AppendLine($"/// </param>");
                return sb;
            }
        );

        // Add cancellationToken param doc for async methods
        var isAsync = (field.Type.IsLeaf() || field.Type.IsList()) && !field.Type.IsInterface();
        if (isAsync)
        {
            builder.AppendLine("/// <param name=\"cancellationToken\">");
            builder.AppendLine(
                "/// A cancellation token that can be used to cancel the operation."
            );
            builder.AppendLine("/// </param>");
        }

        return builder.ToString();
    }

    private static string RenderDocComment(InputValue field)
    {
        return RenderSummaryDocComment(field.Description, field.Name);
    }

    private static string RenderSummaryDocComment(string doc, string fallbackName = "")
    {
        string content;

        if (string.IsNullOrEmpty(doc))
        {
            // Provide a default summary based on the name if no description is available
            content = string.IsNullOrEmpty(fallbackName)
                ? "/// No description available."
                : $"/// {System.Security.SecurityElement.Escape(fallbackName)}";
        }
        else
        {
            var description = doc.Split('\n')
                .Select(line => $"/// {System.Security.SecurityElement.Escape(line)}")
                .Select(line => line.Trim());
            content = string.Join("\n", description);
        }

        return $"""
            /// <summary>
            {content}
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
            return $"{GetNormalizedTypeName(tr.OfType, name)}[]";
        }

        var type = tr.GetTypeName();
        if (type.EndsWith("Id") && !string.Equals(name, "id"))
        {
            type = type.Replace("Id", "");
        }

        return type;
    }

    private static string RenderArgument(InputValue arg)
    {
        return $"{GetNormalizedTypeName(arg)} {arg.GetVarName()}";
    }

    internal static string RenderOptionalArgument(InputValue arg)
    {
        var baseType = GetNormalizedTypeName(arg);
        var nullableType = baseType + "?";

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
                return $"{nullableType} {arg.GetVarName()} = {baseType}.{arg.DefaultValue}";
            }

            // For boolean types with non-null default values, use non-nullable type
            if (baseType == "bool" && (arg.DefaultValue == "true" || arg.DefaultValue == "false"))
            {
                return $"{baseType} {arg.GetVarName()} = {arg.DefaultValue}";
            }

            return $"{nullableType} {arg.GetVarName()} = {arg.DefaultValue}";
        }

        return $"{nullableType} {arg.GetVarName()} = null";
    }

    private static string RenderReturnType(TypeRef type)
    {
        if (type.IsLeaf() || type.IsList())
        {
            return $"async Task<{type.GetTypeName()}>";
        }

        return type.GetTypeName();
    }

    private static string RenderReturnValue(Field field)
    {
        var type = field.Type;

        if (type.IsLeaf())
        {
            return $"await QueryExecutor.ExecuteAsync<{field.Type.GetTypeName()}>(GraphQLClient, queryBuilder, cancellationToken)";
        }

        if (type.IsList() && type.GetType_().OfType.IsObject())
        {
            var typeName = type.GetType_().OfType.GetTypeName();
            return $"""
                (await QueryExecutor.ExecuteListAsync<{typeName}Id>(GraphQLClient, queryBuilder, cancellationToken))
                    .Select(id =>
                        new {typeName}(
                            QueryBuilder.Builder().Select("load{typeName}FromID", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id.Value)))),
                            GraphQLClient
                        )
                    )
                    .ToArray()
                """;
        }

        if (type.IsList() && type.GetType_().OfType.IsScalar())
        {
            var typeName = type.GetType_().OfType.GetTypeName();
            return $"await QueryExecutor.ExecuteListAsync<{typeName}>(GraphQLClient, queryBuilder, cancellationToken)";
        }

        // Interface types are returned like object types
        if (type.IsInterface() || type.IsObject())
        {
            return $"new {field.Type.GetTypeName()}(queryBuilder, GraphQLClient)";
        }

        return $"new {field.Type.GetTypeName()}(queryBuilder, GraphQLClient)";
    }

    private object RenderArgumentBuilder(Field field)
    {
        if (field.Args.Length == 0)
        {
            return string.Empty;
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
                                $"""if ({varName} is {GetNormalizedTypeName(arg)} {varName}_)"""
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

    internal static string RenderArgumentValue(
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
                    // // a type but needs to convert into id value before sending it.
                    if (type.EndsWith("Id") && !string.Equals(arg.Name, "id"))
                    {
                        return $"new IdValue<{type}>({argName})";
                    }

                    // Id type.
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
            var tr = arg.Type.GetType_().OfType.GetType_();

            if (tr.IsScalar())
            {
                var type = tr.GetType_().GetTypeName();
                var token = SyntaxFacts.GetKeywordKind(type);

                var value = token switch
                {
                    SyntaxKind.StringKeyword => "new StringValue(v)",
                    SyntaxKind.IntKeyword => "new IntValue(v)",
                    SyntaxKind.FloatKeyword => "new FloatValue(v)",
                    SyntaxKind.BoolKeyword => "new BooleanValue(v)",
                    _ => (type.EndsWith("Id") && !string.Equals(arg.Name, "id"))
                        ? $"new IdValue<{type}>(v)"
                        : "new StringValue(v.Value)",
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
        if (field.Type.IsList() && !field.Type.GetType_().OfType.IsLeaf())
        {
            builder.Append(".Select(\"id\")");
        }

        builder.Append(';');
        return builder.ToString();
    }

    public string RenderInterface(Type type)
    {
        var interfaceName = Formatter.FormatType(type.Name);
        var wrapperName = $"{interfaceName}Wrapper";

        // Build methods for interface
        var interfaceMethods = new StringBuilder();
        var wrapperMethods = new StringBuilder();

        foreach (var field in type.Fields ?? Array.Empty<Field>())
        {
            var returnType = field.Type.GetTypeName();
            var methodName = Formatter.FormatProperty(field.Name);

            // Build parameter list
            var parameters =
                field
                    .Args?.Select(arg =>
                    {
                        var paramType = arg.Type.GetTypeName();
                        var paramName = Formatter.FormatVarName(arg.Name);
                        return $"{paramType} {paramName}";
                    })
                    .ToList()
                ?? new List<string>();

            var paramList = string.Join(", ", parameters);

            // Determine if method should be async
            bool isAsync = !IsChainableObjectType(field.Type);

            if (isAsync && !returnType.StartsWith("Task<"))
            {
                returnType = $"Task<{returnType}>";
            }

            // Interface method signature
            interfaceMethods.AppendLine($"    {returnType} {methodName}({paramList});");
            interfaceMethods.AppendLine();

            // Wrapper method implementation
            wrapperMethods.AppendLine($"    public {returnType} {methodName}({paramList})");
            wrapperMethods.AppendLine("    {");

            // Build GraphQL query
            wrapperMethods.AppendLine(
                $"        var q = _query.Select(\"{LoadFromIdMethodName(interfaceName)}\");"
            );
            wrapperMethods.AppendLine("        q = q.Arg(\"id\", _id);");
            wrapperMethods.AppendLine($"        q = q.Select(\"{field.Name}\");");

            // Add arguments
            if (field.Args?.Length > 0)
            {
                foreach (var arg in field.Args)
                {
                    var argName = Formatter.FormatVarName(arg.Name);
                    wrapperMethods.AppendLine($"        q = q.Arg(\"{arg.Name}\", {argName});");
                }
            }

            // Execute query and return
            if (isAsync)
            {
                wrapperMethods.AppendLine(
                    $"        return q.Execute<{field.Type.GetType_().GetTypeName()}>();"
                );
            }
            else
            {
                // Chainable object type
                var objectType = field.Type.GetTypeName();
                wrapperMethods.AppendLine($"        return new {objectType}(q);");
            }

            wrapperMethods.AppendLine("    }");
            wrapperMethods.AppendLine();
        }

        return $$"""
            {{RenderDocComment(type)}}
            [Interface]
            public interface {{interfaceName}}
            {
            {{interfaceMethods}}
            }

            /// <summary>
            /// Wrapper for {{interfaceName}} interface.
            /// This wrapper is used when receiving {{interfaceName}} instances from other modules.
            /// </summary>
            /// <param name="query">The query builder.</param>
            /// <param name="id">The ID of the {{interfaceName}} instance.</param>
            public class {{wrapperName}}(Query query, string id) : {{interfaceName}}
            {
                private readonly Query _query = query;
                private readonly string _id = id;

            {{wrapperMethods}}
            }
            """;
    }

    private bool IsChainableObjectType(TypeRef typeRef)
    {
        // Check if the type is an object type (chainable)
        // vs primitive/list (requires async/error handling)
        var type = typeRef.GetType_();
        return type.Kind == Types.TypeKind.OBJECT && !type.Name.EndsWith("Id");
    }

    private string LoadFromIdMethodName(string typeName)
    {
        // Generate loadFromID method name directly from type name
        return $"load{typeName}FromID";
    }
}
