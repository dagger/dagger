using System;
using System.IO;
using System.Reflection;

namespace Dagger.SDK.SourceGenerator.Tests.Utils;

/// <summary>
/// Files embedded into the test assembly (see the test project).
/// </summary>
public static class Fixtures
{
    public static string Read(string name)
    {
        using var stream =
            Assembly.GetExecutingAssembly().GetManifestResourceStream(name)
            ?? throw new InvalidOperationException($"Missing embedded fixture {name}.");
        using var reader = new StreamReader(stream);
        return reader.ReadToEnd();
    }
}
