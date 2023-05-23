namespace DaggerSDK.GraphQL.QueryElements;

public class WithExec : GraphQLElement
{
    public WithExec(string[] args)
    {
        Name = "withExec";
        if (args != null)
        {
            Params.Add("args", args);
        }
    }

    public WithExec(ArgList args, IEnumerable<GraphQLElement>? sub = null)
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

    public class ArgList : List<string>
    {
        public ArgList(params string[] args)
        {
            AddRange(args);
        }
    }
}
