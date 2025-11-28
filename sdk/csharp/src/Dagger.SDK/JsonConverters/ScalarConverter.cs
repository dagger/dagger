using System.Text.Json;
using System.Text.Json.Serialization;

namespace Dagger.JsonConverters;

/// <summary>
/// JSON converter for Dagger scalar ID types.
/// </summary>
/// <typeparam name="TScalar">The scalar type to convert.</typeparam>
public class ScalarIdConverter<TScalar> : JsonConverter<TScalar>
    where TScalar : Scalar, new()
{
    /// <summary>
    /// Reads a scalar ID from JSON.
    /// </summary>
    /// <param name="reader">The JSON reader.</param>
    /// <param name="typeToConvert">The type being converted.</param>
    /// <param name="options">Serialization options.</param>
    /// <returns>The deserialized scalar.</returns>
    public override TScalar? Read(
        ref Utf8JsonReader reader,
        Type typeToConvert,
        JsonSerializerOptions options
    )
    {
        var s = new TScalar { Value = reader.GetString()! };
        return s;
    }

    /// <summary>
    /// Writes a scalar ID to JSON.
    /// </summary>
    /// <param name="writer">The JSON writer.</param>
    /// <param name="scalar">The scalar to serialize.</param>
    /// <param name="options">Serialization options.</param>
    public override void Write(
        Utf8JsonWriter writer,
        TScalar scalar,
        JsonSerializerOptions options
    ) => writer.WriteStringValue(scalar.Value);
}
