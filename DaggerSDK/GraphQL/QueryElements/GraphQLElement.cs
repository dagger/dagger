namespace DaggerSDK.GraphQL.QueryElements;

public class GraphQLElement
{
    public string Label { get; set; } = "";
    public string Name { get; set; } = "";
    public Dictionary<string, object> Params { get; set; } = new();
    public List<GraphQLElement> Body { get; set; } = new();

    public static explicit operator GraphQLElement(string str)
    {
        return new GraphQLElement { Name = str };
    }
}
