using System.Text;
using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Unicode;
using DaggerSDK.GraphQL.QueryElements;

namespace DaggerSDK.GraphQL;

public class Serializer
{
    public static string Serialize(params GraphQLElement[] elements)
    {
        var result = new StringBuilder();
        result.Append("query {");
        PrintIndented(result, elements);
        result.Append("\n}");
        return result.ToString();
    }

    private static void PrintIndented(StringBuilder result, GraphQLElement[] elements, int indent = 1)
    {
        if (elements.Length == 0)
        {
            return;
        }

        var prefix = "\n";
        foreach (var e in elements)
        {
            result.Append(prefix);
            prefix = ",\n";
            PrintIndented(result, e, indent);
        }
    }
    private static void PrintIndented(StringBuilder result, GraphQLElement element, int indent)
    {
        AddIndent(result, indent);
        if (!string.IsNullOrEmpty(element.Label))
        {
            result.Append(element.Label).Append(": ");
        }

        result.Append(element.Name);
        AddArgs(result, element.Params);

        if (!element.Body.Any())
        {
            return;
        }

        result.Append(" {");

        PrintIndented(result, element.Body.ToArray(), indent + 1);

        result.Append("\n");
        AddIndent(result, indent);
        result.Append("}");
    }

    private static void AddArgs(StringBuilder result, Dictionary<string, object> args)
    {
        if (args.Count < 1)
        {
            return;
        }

        result.Append("(");
        var prefix = "";
        foreach (var (k, v) in args)
        {
            result.Append(prefix);
            prefix = ", ";
            result.Append($"{k}: {JsonSerializer.Serialize(v)}");
        }
        result.Append(")");

    }

    private static void AddIndent(StringBuilder result, int indent)
    {
        for (int i = 0; i < indent; i++)
        {
            result.Append("  ");
        }
    }
}
