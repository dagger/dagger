namespace DaggerSDK.GraphQL.QueryElements;

public class Container : GraphQLElement
{
    public Container(string? platform = null, IEnumerable<GraphQLElement>? sub = null)
    {
        Name = "container";
        if (!string.IsNullOrEmpty(platform))
        {
            Params.Add("platform", platform);
        }

        foreach (var element in sub ?? Enumerable.Empty<GraphQLElement>())
        {
            Body.Add(element);
        }
    }
}
