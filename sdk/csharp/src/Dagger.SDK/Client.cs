using Dagger.GraphQL;

namespace Dagger;

/// <summary>
/// Provides access to the root Dagger query.
/// </summary>
public static class Client
{
    private static readonly Lock Gate = new();
    private static volatile Query? _query;

    /// <summary>
    /// Gets a singleton <see cref="Query"/> instance for building DAG operations.
    /// The connection is made on first use; a failed attempt is retried on the next access.
    /// </summary>
    public static Query Dag
    {
        get
        {
            if (_query is { } query)
            {
                return query;
            }

            lock (Gate)
            {
                return _query ??= new Query(QueryBuilder.Builder(), new GraphQLClient());
            }
        }
    }
}
