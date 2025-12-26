using Dagger.GraphQL;

namespace Dagger;

/// <summary>
/// Provides access to the root Dagger query.
/// </summary>
public static class Client
{
    private static readonly Lazy<Query> _query = new(valueFactory: static () =>
    {
        return new Query(QueryBuilder.Builder(), new GraphQLClient());
    });

    /// <summary>
    /// Gets a singleton <see cref="Query"/> instance for building DAG operations.
    /// </summary>
    public static Query Dag => _query.Value;
}
