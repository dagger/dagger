using Dagger;

/// <summary>
/// Example demonstrating experimental API usage with [Experimental] attributes.
/// Shows how to use experimental features and handle compiler diagnostics.
/// </summary>
[Object]
public class ExperimentalExample
{
    /// <summary>
    /// Example demonstrating experimental API usage with Directory.WithPatch.
    /// Uses #pragma warning to suppress only the specific DAGGER_DIRECTORY_WITHPATCH diagnostic.
    /// Each experimental API has a unique diagnostic ID for granular control.
    /// </summary>
    [Function]
    public async Task<string> PatchExample()
    {
        // Create a simple directory with a file
        var dir = Dag.Directory().WithNewFile("hello.txt", "Hello, World!");

        // Apply a patch using experimental API
        var patch =
            @"diff --git a/hello.txt b/hello.txt
index 1234567..abcdef8 100644
--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-Hello, World!
+Hello, Dagger!";

#pragma warning disable DAGGER_DIRECTORY_WITHPATCH // Suppress only WithPatch warnings
        var patched = dir.WithPatch(patch);
#pragma warning restore DAGGER_DIRECTORY_WITHPATCH

        return await patched.File("hello.txt").Contents();
    }

    /// <summary>
    /// Example demonstrating experimental API with Directory.WithPatchFile.
    /// Uses #pragma warning to suppress only the specific DAGGER_DIRECTORY_WITHPATCHFILE diagnostic.
    /// </summary>
    [Function]
    public Directory PatchFileExample()
    {
        var dir = Dag.Directory().WithNewFile("test.txt", "original content");

        var patchFile = Dag.Directory()
            .WithNewFile("changes.patch", "diff --git a/test.txt b/test.txt\n...")
            .File("changes.patch");

#pragma warning disable DAGGER_DIRECTORY_WITHPATCHFILE // Suppress only WithPatchFile warnings
        // Use experimental WithPatchFile API
        return dir.WithPatchFile(patchFile);
#pragma warning restore DAGGER_DIRECTORY_WITHPATCHFILE
    }
}
