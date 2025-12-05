using System.Threading.Tasks;
using Dagger;

[Object]
public class ProcessorImpl
{
    [Field]
    public string Text { get; set; } = "";

    [Function]
    public Task<string> Process(string input)
    {
        return Task.FromResult($"{Text}{input}");
    }

    [Function]
    public ProcessorImpl WithPrefix(string prefix)
    {
        return new ProcessorImpl { Text = prefix };
    }
}
