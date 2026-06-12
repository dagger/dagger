using Dagger.SDK.JsonConverters;
using System.Text.Json.Serialization;

namespace Dagger.SDK;

/// <summary>
/// The unified ID scalar type. All Dagger objects have an ID of this type.
/// </summary>
[JsonConverter(typeof(ScalarIdConverter<Id>))]
public class Id : Scalar
{
}

/// <summary>
/// Interface for objects that have a unique ID.
/// All Dagger objects implement this.
/// </summary>
public interface IId
{
    Task<Id> IdAsync(CancellationToken cancellationToken = default);
}
