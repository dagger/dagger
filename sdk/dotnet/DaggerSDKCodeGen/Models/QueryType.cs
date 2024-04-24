namespace DaggerSDKCodeGen.Models;

public class QueryType
{
    public string? Description { get; set; }
    public EnumType[]? EnumValues { get; set; }
    public QueryField[]? Fields { get; set; }
    public InputField[]? InputFields { get; set; }
    public string[]? Interfaces { get; set; }
    public string? Kind { get; set; }
    public string? Name { get; set; }
    public string[]? PossibleTypes { get; set; }
}
