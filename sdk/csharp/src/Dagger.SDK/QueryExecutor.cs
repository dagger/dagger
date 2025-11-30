using System.Collections.Immutable;
using System.Text.Json;
using Dagger.Exceptions;
using Dagger.GraphQL;

namespace Dagger;

/// <summary>
/// Executes GraphQL queries and deserializes results.
/// </summary>
public static class QueryExecutor
{
    private static readonly JsonSerializerOptions DeserializerOptions = new() 
    { 
        PropertyNameCaseInsensitive = true 
    };

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

        try
        {
            var response = await client.RequestAsync(query, cancellationToken);
            var data = await response.Content.ReadAsStringAsync(cancellationToken);
            var graphQLResponse = JsonSerializer.Deserialize<GraphQLResponse>(data, DeserializerOptions);

            // Check for GraphQL errors in the response
            if (graphQLResponse?.Errors != null && graphQLResponse.Errors.Count > 0)
            {
                ThrowAppropriateException(graphQLResponse.Errors, query);
            }

            return graphQLResponse?.Data ?? new JsonElement();
        }
        catch (Exception)
        {
            throw;
        }
    }

    private static void ThrowAppropriateException(List<GraphQLError> errors, string query)
    {
        var firstError = errors[0];
        var errorType = firstError.ErrorType;

        // Check for EXEC_ERROR type
        if (errorType == "EXEC_ERROR" && firstError.Extensions != null)
        {
            var cmd = new List<string>();
            if (
                firstError.Extensions.TryGetValue("cmd", out var cmdElement)
                && cmdElement.ValueKind == JsonValueKind.Array
            )
            {
                foreach (var item in cmdElement.EnumerateArray())
                {
                    if (item.ValueKind == JsonValueKind.String)
                    {
                        cmd.Add(item.GetString()!);
                    }
                }
            }

            var exitCode = firstError.Extensions.TryGetValue("exitCode", out var exitCodeElement)
                ? exitCodeElement.GetInt32()
                : -1;

            var stdout = firstError.Extensions.TryGetValue("stdout", out var stdoutElement)
                ? stdoutElement.GetString() ?? ""
                : "";

            var stderr = firstError.Extensions.TryGetValue("stderr", out var stderrElement)
                ? stderrElement.GetString() ?? ""
                : "";

            throw new ExecException(
                firstError.Message,
                errors,
                query,
                cmd,
                exitCode,
                stdout,
                stderr
            );
        }

        // Generic GraphQL error
        throw new QueryException(firstError.Message, errors, query);
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
