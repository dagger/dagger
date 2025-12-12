using Dagger;

/// <summary>
/// A module for HelloWithChecksCs functions
/// </summary>
[Object]
public class HelloWithChecksCs
{
    /// <summary>
    /// Returns a passing check
    /// </summary>
    [Function]
    [Check]
    public async Task PassingCheck()
    {
        await Dag.Container().From("alpine:3").WithExec(["sh", "-c", "exit 0"]).Sync();
    }

    /// <summary>
    /// Returns a failing check
    /// </summary>
    [Function]
    [Check]
    public async Task FailingCheck()
    {
        await Dag.Container().From("alpine:3").WithExec(["sh", "-c", "exit 1"]).Sync();
    }
}
