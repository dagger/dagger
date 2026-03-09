using System;
using System.IO;
using System.Text;
using System.Text.Json;
using Dagger.SDK.CodeGen.Code;
using Dagger.SDK.CodeGen.Types;

if (args.Length < 2)
{
    Console.Error.WriteLine("Usage: codegen <introspection.json> <output.cs>");
    return 1;
}

var introspectionPath = args[0];
var outputPath = args[1];

try
{
    if (!File.Exists(introspectionPath))
    {
        Console.Error.WriteLine($"Error: Introspection file not found: {introspectionPath}");
        return 1;
    }

    var json = File.ReadAllText(introspectionPath);
    var introspection = JsonSerializer.Deserialize<Introspection>(json);

    if (introspection == null)
    {
        Console.Error.WriteLine("Error: Failed to deserialize introspection JSON");
        return 1;
    }

    var generator = new CodeGenerator(new CodeRenderer());
    var code = generator.Generate(introspection);

    File.WriteAllText(outputPath, code, Encoding.UTF8);

    Console.WriteLine($"Generated {outputPath} ({introspection.Schema.Types.Length} types)");
    return 0;
}
catch (Exception ex)
{
    Console.Error.WriteLine($"Error: {ex.Message}");
    Console.Error.WriteLine(ex.StackTrace);
    return 1;
}
