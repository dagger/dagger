using Dagger.SDK.GraphQL;

namespace Dagger.SDK;

public static class Dagger
{
    static readonly Lazy<Query> Query = new(
        () => new Query(QueryBuilder.Builder(), new GraphQLClient())
    );

    // <summary>
    // Get a Query instance to start building a dag.
    // </summary>
    public static Query Dag() => Query.Value;
}
