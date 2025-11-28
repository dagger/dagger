using Dagger.GraphQL;

namespace Dagger;

/// <summary>
/// Base class for all Dagger object types.
/// </summary>
/// <param name="queryBuilder">The GraphQL query builder.</param>
/// <param name="gqlClient">The GraphQL client.</param>
public class Object(QueryBuilder queryBuilder, GraphQLClient gqlClient)
{
    /// <summary>
    /// The GraphQL query builder for this object.
    /// </summary>
    public QueryBuilder QueryBuilder { get; } = queryBuilder;
    
    /// <summary>
    /// The GraphQL client for executing queries.
    /// </summary>
    public GraphQLClient GraphQLClient { get; } = gqlClient;
}
