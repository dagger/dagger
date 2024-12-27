using System.Collections.Immutable;
using System.Text.Json;
using Dagger.SDK.GraphQL;

namespace Dagger.SDK;

public static class QueryExecutor
{
    /// <summary>
    /// Execute a GraphQL request and deserialize data into `T`.
    /// </summary>
    /// <typeparam name="T"></typeparam>
    /// <param name="client">A GraphQL client.</param>
    /// <param name="queryBuilder">A QueryBuilder instance.</param>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns></returns>
    public static async Task<T> ExecuteAsync<T>(
        GraphQLClient client,
        QueryBuilder queryBuilder,
        CancellationToken cancellationToken = default
    )
    {
        var jsonElement = await RequestAsync(client, queryBuilder, cancellationToken);
        jsonElement = TakeJsonElementUntilLast<T>(jsonElement, queryBuilder.Path);
        return jsonElement.GetProperty(queryBuilder.Path.Last().Name).Deserialize<T>()!;
    }

    /// <summary>
    /// Similar to Execute but return a list of data.
    /// </summary>
    /// <typeparam name="T"></typeparam>
    /// <param name="client">A GraphQL client</param>
    /// <param name="queryBuilder">A QueryBuilder instance.</param>
    /// <param name="cancellationToken">A cancellation token.</param>
    /// <returns></returns>
    public static async Task<T[]> ExecuteListAsync<T>(
        GraphQLClient client,
        QueryBuilder queryBuilder,
        CancellationToken cancellationToken = default
    )
    {
        var jsonElement = await RequestAsync(client, queryBuilder, cancellationToken);
        jsonElement = TakeJsonElementUntilLast<T>(jsonElement, queryBuilder.Path);
        return jsonElement
            .EnumerateArray()
            .Select(elem => elem.GetProperty(queryBuilder.Path.Last().Name))
            .Select(elem => elem.Deserialize<T>()!)
            .ToArray();
    }

    private static async Task<JsonElement> RequestAsync(
        GraphQLClient client,
        QueryBuilder queryBuilder,
        CancellationToken cancellationToken = default
    )
    {
        var query = queryBuilder.Build();
        var response = await client.RequestAsync(query, cancellationToken);
        // TODO: handle error here.
        var data = await response.Content.ReadAsStringAsync(cancellationToken);
        var jsonElement = JsonSerializer.Deserialize<JsonElement>(data);
        return jsonElement.GetProperty("data");
    }

    // Traverse jsonElement until the last element.
    private static JsonElement TakeJsonElementUntilLast<T>(
        JsonElement jsonElement,
        ImmutableList<Field> path
    )
    {
        var json = jsonElement;
        foreach (var fieldName in path.RemoveAt(path.Count - 1).Select(field => field.Name))
        {
            json = json.GetProperty(fieldName);
        }

        return json;
    }
}
