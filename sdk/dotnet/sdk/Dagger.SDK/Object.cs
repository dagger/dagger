using System.Collections.Immutable;
using Dagger.SDK.GraphQL;

namespace Dagger.SDK;

public class Object(QueryBuilder queryBuilder, GraphQLClient gqlClient)
{
    public QueryBuilder QueryBuilder { get; } = queryBuilder;
    public GraphQLClient GraphQLClient { get; } = gqlClient;

    /// <summary>
    /// Create a QueryBuilder that loads an object by ID using node(id:) with an inline fragment.
    /// </summary>
    public static QueryBuilder NodeQueryBuilder(string id, string graphQLTypeName)
    {
        return QueryBuilder.Builder()
            .Select("node", ImmutableList.Create<Argument>(new Argument("id", new StringValue(id))))
            .InlineFragment(graphQLTypeName);
    }
}
