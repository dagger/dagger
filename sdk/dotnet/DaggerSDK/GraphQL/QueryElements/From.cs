namespace DaggerSDK.GraphQL.QueryElements;

public class From : GraphQLElement
{
    public From(string? address, IEnumerable<GraphQLElement>? sub = null)
    {
        Name = "from";
        if (!string.IsNullOrEmpty(address))
        {
            Params.Add("address", address);
        }

        foreach (var element in sub ?? Enumerable.Empty<GraphQLElement>())
        {
            Body.Add(element);
        }
    }
}
