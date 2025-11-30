using System.Threading.Tasks;
using Dagger;

/// <summary>
/// Example demonstrating module composition and interface implementation.
/// This module calls processor-impl which implements IProcessor from interface-example.
/// </summary>
[Object]
public class ConsumerExample
{
    /// <summary>
    /// Shows how constructor parameters work across module boundaries.
    /// </summary>
    [Function]
    public async Task<string> DemoConstructor()
    {
        // Call with default constructor values
        var result1 = await Dag.ConstructorExample().GreetAsync("World");

        // Call with custom constructor values
        var result2 = await Dag.ConstructorExample("Hi", 9000, false).GreetAsync("Alice");

        return $"Default: {result1}\nCustom: {result2}";
    }

    /// <summary>
    /// Shows optional parameters and default values.
    /// </summary>
    [Function]
    public async Task<string> DemoDefaults()
    {
        var dirId = await Dag.DefaultsExample().CreateFile().IdAsync();
        return dirId.ToString();
    }

    /// <summary>
    /// Shows [DefaultPath] and [Ignore] attributes in action.
    /// </summary>
    [Function]
    public async Task<string> DemoAttributes()
    {
        var dir = Dag.CurrentModule().Source();
        var result = await Dag.AttributesExample().AnalyzeSourceAsync(dir);
        return result;
    }

    /// <summary>
    /// Demonstrates calling the multi-file-example module.
    /// </summary>
    [Function]
    public async Task<string> DemoMultiFile()
    {
        var user = Dag.MultiFileExample().CreateUser("Bob", "bob@example.com");
        var formatted = await Dag.MultiFileExample().FormatUserAsync(user);
        return formatted;
    }

    /// <summary>
    /// Demonstrates .AsInterfaceExampleProcessor()
    /// </summary>
    [Function]
    public async Task<string> DemoInterface()
    {
        var text = "Hello World";        
        ProcessorImpl implementationModule = Dag.ProcessorImpl().WithPrefix(">> ");
        InterfaceExample interfaceModule = Dag.InterfaceExample();
        string result = await interfaceModule.ProcessTextAsync(implementationModule.AsInterfaceExampleProcessor(), text);

        return $"Original: {text}\nProcessed: {result}";
    }
  
    /// <summary>
    /// Runs all example demonstrations.
    /// </summary>
    [Function]
    public async Task<string> RunAll()
    {
        var results = "=== Consumer Example - All Demos ===\n\n";

        results += "1. Constructor:\n";
        results += await DemoConstructor() + "\n\n";

        results += "2. Defaults:\n";
        results += await DemoDefaults() + "\n\n";

        results += "3. Attributes:\n";
        results += await DemoAttributes() + "\n\n";

        results += "4. Multi-File:\n";
        results += await DemoMultiFile() + "\n\n";

        results += "5. Interface:\n";
        results += await DemoInterface() + "\n\n";

        return results;
    }
}
