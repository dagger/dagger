using Dagger;

/// <summary>
/// An interface defining a processor with a method to process input strings.
/// Explicitly named 'Processor' to maintain clean API naming.
/// </summary>
[Interface(Name = "Processor")]
public interface IProcessor
{
    /// <summary>
    /// Processes the given input string and returns the processed result.
    /// </summary>
    /// <param name="input">Input string to process</param>
    /// <returns>Processed string</returns>
    [Function]
    Task<string> Process(string input);
}

/// <summary>
/// An example object that uses the IProcessor interface to process text.
/// </summary>
[Object]
public class InterfaceExample
{
    /// <summary>
    /// Processes the given text using the provided IProcessor implementation.
    /// </summary>
    /// <param name="processor">The IProcessor implementation to use for processing.</param>
    /// <param name="text">The text to process.</param>
    /// <returns>The processed text.</returns>
    [Function]
    public async Task<string> ProcessText(IProcessor processor, string text)
    {
        return await processor.Process(text);
    }
}
