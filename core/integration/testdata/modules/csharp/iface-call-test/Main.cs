using System.Threading.Tasks;
using Dagger;

[Interface]
public interface IDuck
{
    [Function]
    Task<string> Quack();
}

[Object]
public class Test
{
    [Function]
    public IDuck GetDuck() => Dag.Mallard().AsInterface<IDuck>();
}
