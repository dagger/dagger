namespace DaggerSDKCodeGen.Models;

public class QueryDirective
{
    public QueryArg[]? Args { get; set; }
    public string? Description { get; set; }
    public string[]? Locations { get; set; }
    public string? Name { get; set; }
}
