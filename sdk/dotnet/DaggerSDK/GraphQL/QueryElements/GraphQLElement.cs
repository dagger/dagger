namespace DaggerSDK.GraphQL.QueryElements;

public class GraphQLElement
{
    public GraphQLElement()
    {

    }

    public GraphQLElement(string name)
    {
        Name = name;
    }

    public string Label { get; set; } = "";
    public string Name { get; set; } = "";
    public Dictionary<string, object> Params { get; set; } = new();
    public List<GraphQLElement> Body { get; set; } = new();
}
