using System.Collections.Concurrent;
using System.Reflection;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Dagger.SDK;

/// <summary>
/// The schema values an enum member stands for, when the member is named by
/// .NET's convention (<c>Tcp</c>, <c>Shared</c>) rather than like the
/// schema value (<c>TCP</c>, <c>SHARED</c>). The first value is the one
/// sent; the others are aliases the API may return for the same member
/// (<c>GZIP</c> for <c>Gzip</c>).
/// </summary>
[AttributeUsage(AttributeTargets.Field)]
public sealed class GraphQLValueAttribute(params string[] values) : Attribute
{
    public string[] Values { get; } = values;
}

/// <summary>
/// Converts enum members to and from the schema values they stand for (see
/// <see cref="GraphQLValueAttribute"/>). Members without the attribute go by
/// their names.
/// </summary>
public static class GraphQLEnum
{
    private static readonly ConcurrentDictionary<Type, Values> Cache = new();

    /// <summary>
    /// The schema value an enum member is sent as.
    /// </summary>
    public static string ToValue(Enum member)
    {
        var values = ValuesOf(member.GetType());
        return values.ByMember.TryGetValue(member, out var value) ? value : member.ToString();
    }

    /// <summary>
    /// The member of an enum type a schema value stands for.
    /// </summary>
    public static T FromValue<T>(string value)
        where T : struct, Enum
    {
        var values = ValuesOf(typeof(T));
        return values.ByValue.TryGetValue(value, out var member)
            ? (T)(object)member
            : Enum.Parse<T>(value);
    }

    private static Values ValuesOf(Type enumType) =>
        Cache.GetOrAdd(
            enumType,
            static type =>
            {
                var byMember = new Dictionary<Enum, string>();
                var byValue = new Dictionary<string, Enum>();
                foreach (var field in type.GetFields(BindingFlags.Public | BindingFlags.Static))
                {
                    var attribute = field.GetCustomAttribute<GraphQLValueAttribute>();
                    if (attribute == null || attribute.Values.Length == 0)
                    {
                        continue;
                    }
                    var member = (Enum)field.GetValue(null)!;
                    byMember.TryAdd(member, attribute.Values[0]);
                    foreach (var value in attribute.Values)
                    {
                        byValue.TryAdd(value, member);
                    }
                }
                return new Values(byMember, byValue);
            }
        );

    private sealed record Values(
        Dictionary<Enum, string> ByMember,
        Dictionary<string, Enum> ByValue
    );
}

/// <summary>
/// Serializes enum members as the schema values they stand for (see
/// <see cref="GraphQLValueAttribute"/>).
/// </summary>
public class GraphQLEnumConverter<T> : JsonConverter<T>
    where T : struct, Enum
{
    public override T Read(
        ref Utf8JsonReader reader,
        Type typeToConvert,
        JsonSerializerOptions options
    ) => GraphQLEnum.FromValue<T>(reader.GetString()!);

    public override void Write(Utf8JsonWriter writer, T value, JsonSerializerOptions options) =>
        writer.WriteStringValue(GraphQLEnum.ToValue(value));
}
