using Dagger.SDK.GraphQL;

namespace Dagger.SDK;

public static class Dagger
{
    static readonly Lazy<Query> Query =
        new(() => new Query(QueryBuilder.Builder(), new GraphQLClient()));

    // <summary>
    // Connect to the Dagger Engine.
    // </summary>
    public static Query Connect() => Query.Value;
}
