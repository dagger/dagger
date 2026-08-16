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
    public Task PassingCheck()
    {
        return Dag.Container().From("alpine:3").WithExec(["sh", "-c", "exit 0"]).Sync();
    }

    /// <summary>
    /// Returns a failing check
    /// </summary>
    [Function]
    [Check]
    public Task FailingCheck()
    {
        return Dag.Container().From("alpine:3").WithExec(["sh", "-c", "exit 1"]).Sync();
    }

    /// <summary>
    /// Runs a container as a passing check
    /// </summary>
    [Function]
    [Check]
    public async Task PassingContainer()
    {
        await Dag.Container().From("alpine:3").WithExec(["sh", "-c", "exit 0"]).Sync();
    }

    /// <summary>
    /// Runs a container as a failing check
    /// </summary>
    [Function]
    [Check]
    public async Task FailingContainer()
    {
        await Dag.Container().From("alpine:3").WithExec(["sh", "-c", "exit 1"]).Sync();
    }

    /// <summary>
    /// Returns the Test object whose checks surface as test:lint / test:unit
    /// </summary>
    [Function]
    public Test Test()
    {
        return new Test();
    }
}

/// <summary>
/// Namespaced checks (test:lint, test:unit), mirroring the go/java fixtures
/// </summary>
[Object]
public class Test
{
    [Function]
    [Check]
    public Task Lint()
    {
        return Dag.Container().From("alpine").WithExec(["sh", "-c", "exit 0"]).Sync();
    }

    [Function]
    [Check]
    public Task Unit()
    {
        return Dag.Container().From("alpine").WithExec(["sh", "-c", "exit 0"]).Sync();
    }
}
