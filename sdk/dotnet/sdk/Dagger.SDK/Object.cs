using Dagger.SDK.GraphQL;

namespace Dagger.SDK;

public class Object(QueryBuilder queryBuilder, GraphQLClient gqlClient)
{
    public QueryBuilder QueryBuilder { get; } = queryBuilder;
    public GraphQLClient GraphQLClient { get; } = gqlClient;
}
