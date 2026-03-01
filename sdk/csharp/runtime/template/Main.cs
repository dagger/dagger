using Dagger;

/// <summary>
/// A generated module for DaggerModule functions
///
/// This module has been generated via dagger init and serves as a reference to
/// basic module structure as you get started with Dagger.
///
/// Two functions have been pre-created. You can modify, delete, or add to them,
/// as needed. They demonstrate usage of arguments and return types using simple
/// echo and grep commands. The functions can be called from the dagger CLI or
/// from one of the SDKs.
///
/// The first line in this comment block is a short description line and the
/// rest is a long description with more detail on the module's purpose or usage,
/// if appropriate. All modules should have a short description.
/// </summary>
[Object]
public class DaggerModule
{
    /// <summary>
    /// Returns a container that echoes whatever string argument is provided
    /// </summary>
    [Function]
    public Container ContainerEcho(string stringArg)
    {
        return Dag.Container().From("alpine:latest").WithExec(new[] { "echo", stringArg });
    }

    /// <summary>
    /// Returns lines that match a pattern in the files of the provided Directory
    /// </summary>
    [Function]
    public async Task<string> GrepDir(Directory directoryArg, string pattern)
    {
        return await Dag.Container()
            .From("alpine:latest")
            .WithMountedDirectory("/mnt", directoryArg)
            .WithWorkdir("/mnt")
            .WithExec(new[] { "grep", "-R", pattern, "." })
            .Stdout();
    }
}
