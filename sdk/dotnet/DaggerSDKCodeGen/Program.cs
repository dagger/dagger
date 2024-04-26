using DaggerSDK.GraphQL;
using DaggerSDKCodeGen;
using DaggerSDKCodeGen.Models;
using System.Text.Json;

var opt = new JsonSerializerOptions
{
    PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
    WriteIndented = true,
};

var client = new GraphQLClient();
var resp = await client.RequestAsync(IntrospectionQuery.Query);

var body = await resp.Content.ReadAsStringAsync();
var doc = JsonDocument.Parse(body);
var schema = doc.RootElement.GetProperty("data").GetProperty("__schema");

var directives = JsonSerializer.Deserialize<List<QueryDirective>>(schema.GetProperty("directives"), opt)
    ?? throw new Exception("Failed to deserialize directives");
var types = JsonSerializer.Deserialize<List<QueryType>>(schema.GetProperty("types"), opt)
    ?? throw new Exception("Failed to deserialize types"); ;

Console.WriteLine("Writing introspect-api.json");
File.WriteAllText("introspect-api.json", JsonSerializer.Serialize(new
{
    directives = schema.GetProperty("directives"),
    types = schema.GetProperty("types"),
}, opt));

Console.WriteLine("Writing introspect-resparsedult.json");
File.WriteAllText("introspect-parsed.json", JsonSerializer.Serialize(new
{
    directives,
    types,
}, opt));

Console.WriteLine("Directives extracted: {0}", directives.Count);
Console.WriteLine("Types extracted: {0}", types.Count);
