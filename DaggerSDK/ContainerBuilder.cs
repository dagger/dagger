using DaggerSDK.GraphQL;
using DaggerSDK.GraphQL.QueryElements;

namespace DaggerSDK;

public class ContainerBuilder
{
    public string Platform { get; set; } = "";
    public string BaseImage { get; set; } = "";
    public CommandArgs[] Commands { get; set; } = new CommandArgs[0];

    public GraphQLElement GetQuery()
    {
        var result = new Container(platform: Platform, new[]
        {
            new From(BaseImage)
        });

        var curr = result.Body[0];
        foreach (var c in Commands)
        {
            var sub = new WithExec(c.args);
            curr.Body.Add(sub);
            curr = sub;
        }

        curr.Body.Add((GraphQLElement)"stdout");
        curr.Body.Add((GraphQLElement)"stderr");
        return result;
    }

    public class CommandArgs
    {
        public string[] args { get; }

        public CommandArgs(params string[] args)
        {
            this.args = args;
        }
    }
}
