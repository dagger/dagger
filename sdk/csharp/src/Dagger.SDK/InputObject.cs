using Dagger.GraphQL;

namespace Dagger;

/// <summary>
/// Interface for GraphQL input object types.
/// </summary>
public interface IInputObject
{
    /// <summary>
    /// Converts this input object to GraphQL key-value pairs.
    /// </summary>
    /// <returns>A list of key-value pairs representing the input object fields.</returns>
    List<KeyValuePair<string, Value>> ToKeyValuePairs();
}
