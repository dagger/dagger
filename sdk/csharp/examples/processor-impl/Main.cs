using Dagger;

/// <summary>
/// A simple processor implementation that appends a prefix to input strings.
/// </summary>
[Object]
public class ProcessorImpl
{
    /// <summary>
    /// The prefix text to append to input strings. Defaults to an empty string.
    /// </summary>
    [Function]
    public string Text { get; private set; } = "";

    /// <summary>
    /// Processes the input string by appending it to the Text prefix.
    /// </summary>
    /// <param name="input">Input string to process</param>
    /// <returns>Processed string with prefix</returns>
    [Function]
    public Task<string> Process(string input)
    {
        return Task.FromResult($"{Text}{input}");
    }

    /// <summary>
    /// Creates a new ProcessorImpl with the specified prefix.
    /// </summary>
    /// <param name="prefix">Prefix to set on the new ProcessorImpl</param>
    /// <returns>New ProcessorImpl instance with the specified prefix</returns>
    [Function]
    public ProcessorImpl WithPrefix(string prefix)
    {
        Text = prefix;
        return this;
    }
}
