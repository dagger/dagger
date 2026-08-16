using System.Collections.Immutable;
using System.Reflection;
using System.Text.Json;
using System.Text.Json.Serialization;
using Dagger.GraphQL;
using DaggerObject = Dagger.Object;

namespace Dagger.ModuleRuntime;

/// <summary>
/// Serializes Dagger values held in module state as id strings, and rehydrates
/// them on the way back in:
/// - Dagger client objects (anything deriving <see cref="Dagger.Object"/>,
///   including generated interface wrappers) are written as their id and read
///   back as an instance positioned at the Relay-style node lookup.
/// - Module interface values ([Interface]-attributed interfaces) are written
///   as their id (proxy or wrapper) and read back as a dynamic proxy.
/// The engine expects exactly this shape when attaching object/interface
/// fields ("expected string, got map" otherwise).
/// </summary>
internal sealed class DaggerValueConverterFactory : JsonConverterFactory
{
    public override bool CanConvert(Type typeToConvert)
    {
        if (typeof(DaggerObject).IsAssignableFrom(typeToConvert))
        {
            return true;
        }

        return typeToConvert.IsInterface
            && typeToConvert.GetCustomAttribute<InterfaceAttribute>() is not null;
    }

    public override JsonConverter CreateConverter(Type typeToConvert, JsonSerializerOptions options)
    {
        var converterType = typeof(DaggerObject).IsAssignableFrom(typeToConvert)
            ? typeof(DaggerObjectIdConverter<>).MakeGenericType(typeToConvert)
            : typeof(DaggerInterfaceIdConverter<>).MakeGenericType(typeToConvert);
        return (JsonConverter)Activator.CreateInstance(converterType)!;
    }
}

internal sealed class DaggerObjectIdConverter<T> : JsonConverter<T>
    where T : DaggerObject
{
    public override T? Read(
        ref Utf8JsonReader reader,
        Type typeToConvert,
        JsonSerializerOptions options
    )
    {
        var id = ReadId(ref reader);
        if (id is null)
        {
            return null;
        }

        // Reconstruct the object positioned at the node lookup, exactly like
        // object-typed function arguments.
        var queryBuilder = QueryBuilder
            .Builder()
            .Select("node", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id))))
            .Select("... on " + typeToConvert.Name);
        return (T)Activator.CreateInstance(typeToConvert, queryBuilder, Client.Dag.GraphQLClient)!;
    }

    public override void Write(Utf8JsonWriter writer, T value, JsonSerializerOptions options)
    {
        writer.WriteStringValue(DaggerValueIds.IdOf(value));
    }

    private static string? ReadId(ref Utf8JsonReader reader)
    {
        switch (reader.TokenType)
        {
            case JsonTokenType.Null:
                return null;
            case JsonTokenType.String:
                return reader.GetString();
            case JsonTokenType.StartObject:
            {
                // Tolerate { "id": "..." } shapes.
                string? id = null;
                while (reader.Read() && reader.TokenType != JsonTokenType.EndObject)
                {
                    if (
                        reader.TokenType == JsonTokenType.PropertyName
                        && reader.GetString() == "id"
                    )
                    {
                        reader.Read();
                        id = reader.GetString();
                    }
                    else
                    {
                        reader.Skip();
                    }
                }
                return id;
            }
            default:
                throw new JsonException(
                    $"Cannot read a Dagger value id from token {reader.TokenType}."
                );
        }
    }
}

internal sealed class DaggerInterfaceIdConverter<T> : JsonConverter<T>
    where T : class
{
    public override T? Read(
        ref Utf8JsonReader reader,
        Type typeToConvert,
        JsonSerializerOptions options
    )
    {
        if (reader.TokenType == JsonTokenType.Null)
        {
            return null;
        }

        string? id;
        if (reader.TokenType == JsonTokenType.String)
        {
            id = reader.GetString();
        }
        else if (reader.TokenType == JsonTokenType.StartObject)
        {
            id = null;
            while (reader.Read() && reader.TokenType != JsonTokenType.EndObject)
            {
                if (reader.TokenType == JsonTokenType.PropertyName && reader.GetString() == "id")
                {
                    reader.Read();
                    id = reader.GetString();
                }
                else
                {
                    reader.Skip();
                }
            }
        }
        else
        {
            throw new JsonException(
                $"Cannot read an interface value id from token {reader.TokenType}."
            );
        }

        if (string.IsNullOrEmpty(id))
        {
            return null;
        }

        var interfaceName = Entrypoint.ResolveInterfaceGraphQLName(typeof(T));
        return DaggerInterfaceProxy<T>.Create(Client.Dag, interfaceName, id);
    }

    public override void Write(Utf8JsonWriter writer, T value, JsonSerializerOptions options)
    {
        switch (value)
        {
            case IDaggerInterfaceProxy proxy:
                writer.WriteStringValue(proxy.ProxyId);
                break;
            case DaggerObject obj:
                writer.WriteStringValue(DaggerValueIds.IdOf(obj));
                break;
            default:
                throw new JsonException(
                    $"Cannot serialize interface value of type '{value.GetType().Name}': "
                        + "expected a Dagger object or interface proxy."
                );
        }
    }
}

internal static class DaggerValueIds
{
    /// <summary>
    /// Resolves a Dagger object's id. The module runtime is a console app
    /// without a synchronization context, so blocking here is safe.
    /// </summary>
    internal static string IdOf(DaggerObject obj)
    {
        var idMethod =
            obj.GetType().GetMethod("Id", new[] { typeof(CancellationToken) })
            ?? throw new JsonException(
                $"Type '{obj.GetType().Name}' has no Id method; cannot serialize as id."
            );
        var task = (Task)idMethod.Invoke(obj, new object[] { CancellationToken.None })!;
        task.GetAwaiter().GetResult();
        var result = task.GetType().GetProperty("Result")!.GetValue(task);
        return result switch
        {
            Scalar s => s.Value,
            string str => str,
            _ => throw new JsonException(
                $"Unexpected id result type '{result?.GetType().Name}' for '{obj.GetType().Name}'."
            ),
        };
    }
}
