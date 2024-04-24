namespace DaggerSDK.GraphQL.QueryElements;

public class WithExec : GraphQLElement
{
    public WithExec(string[] args, IEnumerable<GraphQLElement>? sub = null)
    {
        Name = "withExec";
        if (args != null)
        {
            Params.Add("args", args);
        }

        foreach (var element in sub ?? Enumerable.Empty<GraphQLElement>())
        {
            Body.Add(element);
        }
    }
}
