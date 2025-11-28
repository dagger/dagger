using Dagger;

/// <summary>
/// Example demonstrating Dagger attributes like [DefaultPath] and [Ignore].
/// These attributes customize how Directory parameters are loaded and filtered.
/// </summary>
[Object]
public class AttributesExample
{
    /// <summary>
    /// Demonstrates [DefaultPath] attribute.
    /// If no directory is provided via CLI, it defaults to current directory (".").
    /// </summary>
    /// <param name="source">Source directory to analyze</param>
    [Function]
    public async Task<string> AnalyzeSource(
        [DefaultPath(".")] 
        Directory source)
    {
        var id = await source.IdAsync();
        return $"Analyzed source directory (ID: {id})";
    }

    /// <summary>
    /// Demonstrates [DefaultPath] with subdirectory.
    /// Will auto-load from 'src' subdirectory if it exists.
    /// </summary>
    /// <param name="srcDir">Source directory</param>
    [Function]
    public async Task<string> AnalyzeSrcFolder(
        [DefaultPath("./src")]
        Directory srcDir)
    {
        var id = await srcDir.IdAsync();
        return $"Analyzed src folder (ID: {id})";
    }

    /// <summary>
    /// Demonstrates working with Directory without Ignore attribute.
    /// Note: [Ignore] attribute has known issues in current SDK version.
    /// </summary>
    [Function]
    public async Task<string> GetDirectoryInfo(
        [DefaultPath(".")]
        Directory dir)
    {
        var id = await dir.IdAsync();
        return $"Directory loaded successfully (ID: {id})";
    }
}
