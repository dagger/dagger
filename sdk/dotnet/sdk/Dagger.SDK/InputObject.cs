using Dagger.SDK.GraphQL;

namespace Dagger.SDK;

public interface IInputObject
{
    List<KeyValuePair<string, Value>> ToKeyValuePairs();
}
