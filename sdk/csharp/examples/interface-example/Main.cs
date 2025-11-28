using System.Threading.Tasks;
using Dagger;

[Interface]
public interface IProcessor
{
    [Function]
    Task<string> Process(string input);
}

[Object]
public class InterfaceExample
{
    [Function]
    public async Task<string> ProcessText(IProcessor processor, string text)
    {
        return await processor.Process(text);
    }
}
