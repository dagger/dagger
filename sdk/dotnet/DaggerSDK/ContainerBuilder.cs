using DaggerSDK.GraphQL;
using DaggerSDK.GraphQL.QueryElements;

namespace DaggerSDK;

public class ContainerBuilder
{
    public string Platform { get; set; } = "";
    public string BaseImage { get; set; } = "";
    public List<string[]> Commands { get; set; } = new();

    public GraphQLElement GetQuery()
    {
        var result = new Container(platform: Platform, new[]
        {
            new From(BaseImage)
        });

        var curr = result.Body[0];
        foreach (var c in Commands)
        {
            var sub = new WithExec(c);
            curr.Body.Add(sub);
            curr = sub;
        }

        curr.Body.Add(new("stdout"));
        curr.Body.Add(new("stderr"));
        return result;
    }
}
