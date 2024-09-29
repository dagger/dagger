using System.Text.Json;
using System.Text.Json.Serialization;

namespace Dagger.SDK.JsonConverters;

// <summary>
// Serialize scalar id string value into scalar id class.
// </summary>
public class ScalarIdConverter<TScalar> : JsonConverter<TScalar>
    where TScalar : Scalar, new()
{
    public override TScalar? Read(
        ref Utf8JsonReader reader,
        Type typeToConvert,
        JsonSerializerOptions options
    )
    {
        var s = new TScalar { Value = reader.GetString()! };
        return s;
    }

    public override void Write(
        Utf8JsonWriter writer,
        TScalar scalar,
        JsonSerializerOptions options
    ) => writer.WriteStringValue(scalar.Value);
}
